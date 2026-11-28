// Package entity holds the workspace permission catalog, tenant custom roles
// and the effective access of one signed-in user.
package entity

import (
	"os"
	"sort"
	"strings"

	iamEntity "api-pacs/module/iam/domain/entity"
)

// Permission identifiers. They are stored in Firestore and returned to the
// viewer, so they must never be renamed.
const (
	PermModelsUse        = "models.use"
	PermFeedbackVote     = "feedback.vote"
	PermResultsViewAll   = "results.view_all"
	PermFeedbackView     = "feedback.view"
	PermMembersInvite    = "members.invite"
	PermMembersManage    = "members.manage"
	PermProcessingManage = "processing.manage"
	PermModelsManage     = "models.manage"
	PermDicomManage      = "dicom.manage"
	PermLogsView         = "logs.view"
	PermWorkspaceManage  = "workspace.manage"
)

// BaselinePermissions are held by every OWNER, ADMIN and USER account.
var BaselinePermissions = []string{PermModelsUse, PermFeedbackVote}

// GrantablePermissions can be added to a USER account through a custom role.
var GrantablePermissions = []string{
	PermResultsViewAll,
	PermFeedbackView,
	PermMembersInvite,
	PermMembersManage,
	PermProcessingManage,
	PermModelsManage,
	PermDicomManage,
	PermLogsView,
	PermWorkspaceManage,
}

// Custom role limits.
const (
	MaxRoleNameLength        = 64
	MaxRoleDescriptionLength = 280
	MaxRoleModelIDs          = 200
	MaxRoleModelIDLength     = 128
)

// AllPermissions returns the full catalog, baseline first.
func AllPermissions() []string {
	all := make([]string, 0, len(BaselinePermissions)+len(GrantablePermissions))
	all = append(all, BaselinePermissions...)
	return append(all, GrantablePermissions...)
}

// IsBaselinePermission reports whether every known account holds permission.
func IsBaselinePermission(permission string) bool {
	return contains(BaselinePermissions, permission)
}

// IsGrantablePermission reports whether a custom role may grant permission.
func IsGrantablePermission(permission string) bool {
	return contains(GrantablePermissions, permission)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// DefaultResearchModelIDs are the model-info modelIds of research-only models
// (for example non-commercial licences). "Every model" access of a USER, from
// the baseline or from a custom role with all_models, does not include them:
// a USER sees one only when its custom role lists it explicitly. OWNER and
// ADMIN keep every model. PACS_RESEARCH_MODEL_IDS (comma-separated) replaces
// the default list; an empty value disables the exclusion.
var DefaultResearchModelIDs = []string{"EchoAlign"}

// ResearchModelIDs returns the research-only model ids in effect.
func ResearchModelIDs() []string {
	value, set := os.LookupEnv("PACS_RESEARCH_MODEL_IDS")
	if !set {
		return DefaultResearchModelIDs
	}
	ids := []string{}
	for _, id := range strings.Split(value, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// normalizedModelName compares recorded names with model-info modelIds: the part
// before a parenthesised qualifier, lower-cased, letters and digits only, so
// "EchoAlign (research use only)" matches "EchoAlign" and "DeepRV-CLIP" matches
// "DeepRV_CLIP", while "EchoAlignment" does not match "EchoAlign".
func normalizedModelName(name string) string {
	if index := strings.Index(name, " ("); index >= 0 {
		name = name[:index]
	}
	var builder strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// researchModelIDForName returns the research modelId a recorded name belongs to, or "".
func researchModelIDForName(name string) string {
	normalized := normalizedModelName(strings.TrimSpace(name))
	if normalized == "" {
		return ""
	}
	for _, id := range ResearchModelIDs() {
		if normalizedModelName(id) == normalized {
			return id
		}
	}
	return ""
}

// IsResearchModelName reports whether a recorded model name (registration or
// study-service name) belongs to a research-only model.
func IsResearchModelName(name string) bool {
	return researchModelIDForName(name) != ""
}

// HidesResearchModels reports whether research-only models are withheld from
// this access (every USER without an explicit allowlist that names them).
func (access Access) HidesResearchModels() bool {
	return !access.Unrestricted()
}

// AllowsModelName reports whether a result recorded under a model name may be
// listed: research-only names follow AllowsModel of their modelId, other names
// keep the previous behaviour (listing does not resolve registrations).
func (access Access) AllowsModelName(name string) bool {
	id := researchModelIDForName(name)
	if id == "" {
		return true
	}
	return access.AllowsModel(id)
}

// IsResearchModel reports whether modelID is research-only.
func IsResearchModel(modelID string) bool {
	return contains(ResearchModelIDs(), strings.TrimSpace(modelID))
}

// CustomRole is a tenant-defined set of extra permissions and model access,
// assigned to USER accounts through users.custom_role_id.
type CustomRole struct {
	ID          string   `firestore:"id"`
	TenantID    string   `firestore:"tenant_id"`
	Name        string   `firestore:"name"`
	Description string   `firestore:"description"`
	Permissions []string `firestore:"permissions"`
	AllModels   bool     `firestore:"all_models"`
	ModelIDs    []string `firestore:"model_ids"`
	CreatedBy   string   `firestore:"created_by"`
	CreatedAt   int64    `firestore:"created_at"`
	UpdatedAt   int64    `firestore:"updated_at"`
	// LastAssignedAt is written by every assignment so that assignment and
	// deletion of the same role serialize in Firestore transactions.
	LastAssignedAt int64 `firestore:"last_assigned_at,omitempty"`
}

// GetModelName returns the Firestore collection of custom roles.
func (CustomRole) GetModelName() string {
	return "tenant_roles"
}

// CustomRoleRef names the custom role behind an access decision.
type CustomRoleRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Access is the effective access of one user in one tenant.
type Access struct {
	Role        string
	permissions map[string]struct{}
	AllModels   bool
	modelIDs    map[string]struct{}
	// excluded holds research-only models withheld from a USER's "every
	// model" access. It is empty for OWNER, ADMIN and allowlist roles.
	excluded   map[string]struct{}
	CustomRole *CustomRoleRef
}

// ForRole computes the access of an account. OWNER and ADMIN hold every
// permission and every model. USER holds the baseline, plus the grantable
// permissions and the model access of its custom role when one is given;
// a USER's "every model" access excludes research-only models.
// Any other role holds nothing.
func ForRole(role string, custom *CustomRole) Access {
	access := Access{Role: role, permissions: map[string]struct{}{}, modelIDs: map[string]struct{}{}, excluded: map[string]struct{}{}}
	switch role {
	case iamEntity.OwnerRole, iamEntity.AdminRole:
		for _, permission := range AllPermissions() {
			access.permissions[permission] = struct{}{}
		}
		access.AllModels = true
	case iamEntity.UserRole:
		for _, permission := range BaselinePermissions {
			access.permissions[permission] = struct{}{}
		}
		access.AllModels = true
		if custom != nil {
			for _, permission := range custom.Permissions {
				if IsGrantablePermission(permission) {
					access.permissions[permission] = struct{}{}
				}
			}
			access.AllModels = custom.AllModels
			if !custom.AllModels {
				for _, modelID := range custom.ModelIDs {
					if modelID = strings.TrimSpace(modelID); modelID != "" {
						access.modelIDs[modelID] = struct{}{}
					}
				}
			}
			access.CustomRole = &CustomRoleRef{ID: custom.ID, Name: custom.Name}
		}
		if access.AllModels {
			for _, modelID := range ResearchModelIDs() {
				access.excluded[modelID] = struct{}{}
			}
		}
	}
	return access
}

// Unrestricted reports whether the access allows every model, research-only
// models included (OWNER and ADMIN).
func (access Access) Unrestricted() bool {
	return access.AllModels && len(access.excluded) == 0
}

// Has reports whether the access holds permission.
func (access Access) Has(permission string) bool {
	_, ok := access.permissions[permission]
	return ok
}

// AllowsModel reports whether the model-info modelId may be used.
func (access Access) AllowsModel(modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if access.AllModels {
		_, excluded := access.excluded[modelID]
		return !excluded
	}
	_, ok := access.modelIDs[modelID]
	return ok
}

// PermissionList returns the held permissions in catalog order.
func (access Access) PermissionList() []string {
	list := []string{}
	for _, permission := range AllPermissions() {
		if access.Has(permission) {
			list = append(list, permission)
		}
	}
	return list
}

// ModelIDList returns the allowlisted model ids, sorted; empty when AllModels.
func (access Access) ModelIDList() []string {
	list := make([]string, 0, len(access.modelIDs))
	if access.AllModels {
		return list
	}
	for modelID := range access.modelIDs {
		list = append(list, modelID)
	}
	sort.Strings(list)
	return list
}
