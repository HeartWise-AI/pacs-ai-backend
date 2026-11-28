// Package service resolves effective access and manages tenant custom roles.
package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/segmentio/ksuid"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/domain/entity"
	domainRepository "api-pacs/module/access/domain/repository"
	iamEntity "api-pacs/module/iam/domain/entity"
)

// DefaultAccessCacheTTL bounds how long a resolved access is reused.
const DefaultAccessCacheTTL = 30 * time.Second

// RoleInput is the editable part of a custom role.
type RoleInput struct {
	Name        string
	Description string
	Permissions []string
	AllModels   bool
	ModelIDs    []string
}

// RoleView is a custom role with its member count.
type RoleView struct {
	entity.CustomRole
	MemberCount int
}

type cachedAccess struct {
	access  entity.Access
	expires time.Time
}

// AccessService resolves the effective access of a user and manages custom
// roles. Resolved access is cached per tenant/user/role for TTL and dropped
// whenever a role of the tenant or the user's assignment changes.
type AccessService struct {
	Repository domainRepository.RoleRepositoryInterface
	TTL        time.Duration
	Now        func() time.Time

	mu    sync.Mutex
	cache map[string]cachedAccess
}

func (service *AccessService) now() time.Time {
	if service.Now != nil {
		return service.Now()
	}
	return time.Now()
}

func (service *AccessService) ttl() time.Duration {
	if service.TTL > 0 {
		return service.TTL
	}
	return DefaultAccessCacheTTL
}

func cacheKey(tenantID, userID, role string) string {
	return tenantID + "\x00" + userID + "\x00" + role
}

// Resolve returns the access of a signed-in user from the role and custom
// role stored on the user record. The role the session was issued with only
// keys the cache: a member whose role changed since sign-in gets the access
// of the stored role. Only USER access is cached, so a cached entry never
// grants OWNER or ADMIN. A custom role that no longer exists (or belongs to
// another tenant) leaves the baseline. Lookup errors are returned so callers
// fail closed.
func (service *AccessService) Resolve(ctx context.Context, tenantID, userID, role string) (entity.Access, error) {
	key := cacheKey(tenantID, userID, role)
	service.mu.Lock()
	if cached, ok := service.cache[key]; ok && service.now().Before(cached.expires) {
		service.mu.Unlock()
		return cached.access, nil
	}
	service.mu.Unlock()

	if service.Repository == nil {
		return entity.Access{}, errors.New(apiError.AccessUnavailable)
	}
	assignment, err := service.Repository.GetUserAssignment(ctx, tenantID, userID)
	if err != nil {
		return entity.Access{}, errors.New(apiError.AccessUnavailable)
	}
	if assignment.Role != iamEntity.UserRole {
		return entity.ForRole(assignment.Role, nil), nil
	}
	var custom *entity.CustomRole
	if roleID := strings.TrimSpace(assignment.CustomRoleID); roleID != "" {
		found, err := service.Repository.GetRole(ctx, tenantID, roleID)
		switch {
		case err == nil:
			custom = &found
		case err.Error() == apiError.MissingRecord:
			// Dangling assignment (the role is gone): keep the baseline
			// permissions but no model, so a lost allowlist never widens
			// into every model.
			custom = &entity.CustomRole{ID: roleID}
		default:
			return entity.Access{}, errors.New(apiError.AccessUnavailable)
		}
	}
	access := entity.ForRole(iamEntity.UserRole, custom)
	if custom != nil && custom.Name == "" && custom.TenantID == "" {
		access.CustomRole = nil
	}

	service.mu.Lock()
	if service.cache == nil {
		service.cache = map[string]cachedAccess{}
	}
	service.cache[key] = cachedAccess{access: access, expires: service.now().Add(service.ttl())}
	service.mu.Unlock()
	return access, nil
}

// InvalidateTenant drops every cached access of the tenant.
func (service *AccessService) InvalidateTenant(tenantID string) {
	prefix := tenantID + "\x00"
	service.mu.Lock()
	defer service.mu.Unlock()
	for key := range service.cache {
		if strings.HasPrefix(key, prefix) {
			delete(service.cache, key)
		}
	}
}

// InvalidateUser drops the cached access of one user.
func (service *AccessService) InvalidateUser(tenantID, userID string) {
	prefix := tenantID + "\x00" + userID + "\x00"
	service.mu.Lock()
	defer service.mu.Unlock()
	for key := range service.cache {
		if strings.HasPrefix(key, prefix) {
			delete(service.cache, key)
		}
	}
}

// normalizeRoleInput validates and canonicalizes a role body. Baseline
// permissions are dropped (every account holds them); unknown permissions
// are rejected.
func normalizeRoleInput(input RoleInput) (RoleInput, error) {
	invalid := errors.New(apiError.InvalidPayload)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > entity.MaxRoleNameLength {
		return RoleInput{}, invalid
	}
	if utf8.RuneCountInString(input.Description) > entity.MaxRoleDescriptionLength {
		return RoleInput{}, invalid
	}
	permissions := []string{}
	seen := map[string]bool{}
	for _, permission := range input.Permissions {
		permission = strings.TrimSpace(permission)
		switch {
		case entity.IsBaselinePermission(permission):
			continue
		case !entity.IsGrantablePermission(permission):
			return RoleInput{}, invalid
		case seen[permission]:
			continue
		}
		seen[permission] = true
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	input.Permissions = permissions

	modelIDs := []string{}
	if !input.AllModels {
		seenModel := map[string]bool{}
		for _, modelID := range input.ModelIDs {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" || len(modelID) > entity.MaxRoleModelIDLength {
				return RoleInput{}, invalid
			}
			if seenModel[modelID] {
				continue
			}
			seenModel[modelID] = true
			modelIDs = append(modelIDs, modelID)
		}
		if len(modelIDs) > entity.MaxRoleModelIDs {
			return RoleInput{}, invalid
		}
		sort.Strings(modelIDs)
	}
	input.ModelIDs = modelIDs
	return input, nil
}

func nameTaken(roles []entity.CustomRole, name, exceptID string) bool {
	for _, role := range roles {
		if role.ID != exceptID && strings.EqualFold(strings.TrimSpace(role.Name), name) {
			return true
		}
	}
	return false
}

func (service *AccessService) memberCounts(ctx context.Context, tenantID string) (map[string]int, error) {
	assignments, err := service.Repository.ListUserAssignments(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, assignment := range assignments {
		if assignment.Role == iamEntity.UserRole && assignment.CustomRoleID != "" {
			counts[assignment.CustomRoleID]++
		}
	}
	return counts, nil
}

// ListRoles returns the tenant's custom roles sorted by name, with member
// counts (USER accounts currently assigned).
func (service *AccessService) ListRoles(ctx context.Context, tenantID string) ([]RoleView, error) {
	roles, err := service.Repository.ListRoles(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}
	counts, err := service.memberCounts(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}
	views := make([]RoleView, 0, len(roles))
	for _, role := range roles {
		views = append(views, RoleView{CustomRole: role, MemberCount: counts[role.ID]})
	}
	sort.Slice(views, func(i, j int) bool {
		return strings.ToLower(views[i].Name) < strings.ToLower(views[j].Name)
	})
	return views, nil
}

// CreateRole validates and stores a new custom role.
func (service *AccessService) CreateRole(ctx context.Context, tenantID, actorID string, input RoleInput) (RoleView, error) {
	input, err := normalizeRoleInput(input)
	if err != nil {
		return RoleView{}, err
	}
	roles, err := service.Repository.ListRoles(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return RoleView{}, err
	}
	if nameTaken(roles, input.Name, "") {
		return RoleView{}, errors.New(apiError.DuplicateRecord)
	}
	now := service.now().Unix()
	role := entity.CustomRole{
		ID: ksuid.New().String(), TenantID: tenantID, Name: input.Name, Description: input.Description,
		Permissions: input.Permissions, AllModels: input.AllModels, ModelIDs: input.ModelIDs,
		CreatedBy: actorID, CreatedAt: now, UpdatedAt: now,
	}
	if err := service.Repository.CreateRole(ctx, role); err != nil {
		return RoleView{}, err
	}
	return RoleView{CustomRole: role}, nil
}

// UpdateRole replaces the editable fields of a custom role.
func (service *AccessService) UpdateRole(ctx context.Context, tenantID, roleID string, input RoleInput) (RoleView, error) {
	input, err := normalizeRoleInput(input)
	if err != nil {
		return RoleView{}, err
	}
	existing, err := service.Repository.GetRole(ctx, tenantID, roleID)
	if err != nil {
		return RoleView{}, err
	}
	roles, err := service.Repository.ListRoles(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return RoleView{}, err
	}
	if nameTaken(roles, input.Name, existing.ID) {
		return RoleView{}, errors.New(apiError.DuplicateRecord)
	}
	existing.Name, existing.Description = input.Name, input.Description
	existing.Permissions, existing.AllModels, existing.ModelIDs = input.Permissions, input.AllModels, input.ModelIDs
	existing.UpdatedAt = service.now().Unix()
	if err := service.Repository.UpdateRole(ctx, existing); err != nil {
		return RoleView{}, err
	}
	service.InvalidateTenant(tenantID)
	counts, err := service.memberCounts(ctx, tenantID)
	if err != nil {
		counts = map[string]int{}
	}
	return RoleView{CustomRole: existing, MemberCount: counts[existing.ID]}, nil
}

// DeleteRole removes a custom role that no USER account holds.
func (service *AccessService) DeleteRole(ctx context.Context, tenantID, roleID string) error {
	if _, err := service.Repository.GetRole(ctx, tenantID, roleID); err != nil {
		return err
	}
	counts, err := service.memberCounts(ctx, tenantID)
	if err != nil {
		return err
	}
	if counts[roleID] > 0 {
		return errors.New(apiError.RoleInUse)
	}
	if err := service.Repository.DeleteRole(ctx, tenantID, roleID); err != nil {
		return err
	}
	service.InvalidateTenant(tenantID)
	return nil
}

// AssignCustomRole sets ("" clears) the custom role of a USER account.
func (service *AccessService) AssignCustomRole(ctx context.Context, tenantID, userID, roleID string) error {
	roleID = strings.TrimSpace(roleID)
	assignment, err := service.Repository.GetUserAssignment(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	if roleID != "" {
		if assignment.Role != iamEntity.UserRole {
			return errors.New(apiError.InvalidPayload)
		}
		if _, err := service.Repository.GetRole(ctx, tenantID, roleID); err != nil {
			return err
		}
	}
	if err := service.Repository.SetUserCustomRole(ctx, tenantID, userID, roleID); err != nil {
		return err
	}
	service.InvalidateUser(tenantID, userID)
	return nil
}

// PermissionCatalog lists every permission and whether it is baseline.
func PermissionCatalog() []PermissionInfo {
	catalog := []PermissionInfo{}
	for _, permission := range entity.AllPermissions() {
		catalog = append(catalog, PermissionInfo{ID: permission, Baseline: entity.IsBaselinePermission(permission)})
	}
	return catalog
}

// PermissionInfo describes one catalog entry.
type PermissionInfo struct {
	ID       string `json:"id"`
	Baseline bool   `json:"baseline"`
}
