// Package entity holds the workspace permission catalog, tenant custom roles
// and the effective access of one signed-in user.
package entity

import (
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
	CustomRole  *CustomRoleRef
}

// ForRole computes the access of an account. OWNER and ADMIN hold every
// permission and every model. USER holds the baseline, plus the grantable
// permissions and the model access of its custom role when one is given.
// Any other role holds nothing.
func ForRole(role string, custom *CustomRole) Access {
	access := Access{Role: role, permissions: map[string]struct{}{}, modelIDs: map[string]struct{}{}}
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
	}
	return access
}

// Has reports whether the access holds permission.
func (access Access) Has(permission string) bool {
	_, ok := access.permissions[permission]
	return ok
}

// AllowsModel reports whether the model-info modelId may be used.
func (access Access) AllowsModel(modelID string) bool {
	if access.AllModels {
		return true
	}
	_, ok := access.modelIDs[strings.TrimSpace(modelID)]
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
