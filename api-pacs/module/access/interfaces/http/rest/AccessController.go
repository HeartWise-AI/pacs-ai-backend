// Package rest exposes the effective access of the signed-in user and the
// tenant custom-role management endpoints.
package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/accessctx"
	accessEntity "api-pacs/module/access/domain/entity"
	accessService "api-pacs/module/access/infrastructure/service"
)

const roleRequestMaxBytes = 64 << 10

// RoleManager is the role management surface used by the controller.
type RoleManager interface {
	ListRoles(ctx context.Context, tenantID string) ([]accessService.RoleView, error)
	CreateRole(ctx context.Context, tenantID, actorID string, input accessService.RoleInput) (accessService.RoleView, error)
	UpdateRole(ctx context.Context, tenantID, roleID string, input accessService.RoleInput) (accessService.RoleView, error)
	DeleteRole(ctx context.Context, tenantID, roleID string) error
	AssignCustomRole(ctx context.Context, tenantID, userID, roleID string) error
}

// AccessController serves /user/me/access and /user/roles.
type AccessController struct {
	Roles RoleManager
}

// AccessResponse is the effective access of the signed-in user.
type AccessResponse struct {
	Role        string                      `json:"role"`
	Permissions []string                    `json:"permissions"`
	AllModels   bool                        `json:"allModels"`
	ModelIDs    []string                    `json:"modelIds"`
	CustomRole  *accessEntity.CustomRoleRef `json:"customRole"`
}

// RoleResponse is one custom role.
type RoleResponse struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	AllModels   bool     `json:"allModels"`
	ModelIDs    []string `json:"modelIds"`
	MemberCount int      `json:"memberCount"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
}

// RoleListResponse lists the tenant's custom roles and the catalog.
type RoleListResponse struct {
	Roles       []RoleResponse                 `json:"roles"`
	Permissions []accessService.PermissionInfo `json:"permissions"`
}

// RoleRequest is the body of role create and update.
type RoleRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	AllModels   bool     `json:"allModels"`
	ModelIDs    []string `json:"modelIds"`
}

// AssignCustomRoleRequest is the body of PUT /user/{ID}/custom-role.
type AssignCustomRoleRequest struct {
	CustomRoleID *string `json:"customRoleId"`
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func toRoleResponse(view accessService.RoleView) RoleResponse {
	return RoleResponse{
		ID: view.ID, Name: view.Name, Description: view.Description,
		Permissions: nonNil(view.Permissions), AllModels: view.AllModels, ModelIDs: nonNil(view.ModelIDs),
		MemberCount: view.MemberCount, CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt,
	}
}

func identity(r *http.Request) (string, string, bool) {
	tenantID, tenantOK := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID, userOK := r.Context().Value(iamTypes.UserIDCtx).(string)
	tenantID, userID = strings.TrimSpace(tenantID), strings.TrimSpace(userID)
	return tenantID, userID, tenantOK && userOK && tenantID != "" && userID != ""
}

// GetMyAccess returns the effective access of the signed-in user.
func (controller *AccessController) GetMyAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, _, ok := identity(r); !ok {
		writeAccessError(w, apiError.UnauthorizedAccess)
		return
	}
	access, err := accessctx.Get(r.Context())
	if err != nil {
		writeAccessError(w, apiError.AccessUnavailable)
		return
	}
	writeAccessSuccess(w, http.StatusOK, "Successfully retrieved access.", AccessResponse{
		Role: access.Role, Permissions: access.PermissionList(), AllModels: access.AllModels,
		ModelIDs: access.ModelIDList(), CustomRole: access.CustomRole,
	})
}

// ListRoles returns the tenant's custom roles and the permission catalog.
func (controller *AccessController) ListRoles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, _, ok := identity(r)
	if !ok {
		writeAccessError(w, apiError.UnauthorizedAccess)
		return
	}
	views, err := controller.Roles.ListRoles(r.Context(), tenantID)
	if err != nil {
		writeAccessError(w, err.Error())
		return
	}
	roles := make([]RoleResponse, 0, len(views))
	for _, view := range views {
		roles = append(roles, toRoleResponse(view))
	}
	writeAccessSuccess(w, http.StatusOK, "Successfully retrieved roles.", RoleListResponse{
		Roles: roles, Permissions: accessService.PermissionCatalog(),
	})
}

func decodeRoleRequest(r *http.Request) (accessService.RoleInput, error) {
	var request RoleRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, roleRequestMaxBytes)).Decode(&request); err != nil {
		return accessService.RoleInput{}, errors.New(apiError.InvalidPayload)
	}
	return accessService.RoleInput{
		Name: request.Name, Description: request.Description, Permissions: request.Permissions,
		AllModels: request.AllModels, ModelIDs: request.ModelIDs,
	}, nil
}

// CreateRole creates a custom role.
func (controller *AccessController) CreateRole(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := identity(r)
	if !ok {
		writeAccessError(w, apiError.UnauthorizedAccess)
		return
	}
	input, err := decodeRoleRequest(r)
	if err != nil {
		writeAccessError(w, err.Error())
		return
	}
	view, err := controller.Roles.CreateRole(r.Context(), tenantID, userID, input)
	if err != nil {
		writeAccessError(w, err.Error())
		return
	}
	writeAccessSuccess(w, http.StatusCreated, "Successfully created role.", toRoleResponse(view))
}

// UpdateRole replaces a custom role.
func (controller *AccessController) UpdateRole(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := identity(r)
	if !ok {
		writeAccessError(w, apiError.UnauthorizedAccess)
		return
	}
	input, err := decodeRoleRequest(r)
	if err != nil {
		writeAccessError(w, err.Error())
		return
	}
	view, err := controller.Roles.UpdateRole(r.Context(), tenantID, chi.URLParam(r, "roleID"), input)
	if err != nil {
		writeAccessError(w, err.Error())
		return
	}
	writeAccessSuccess(w, http.StatusOK, "Successfully updated role.", toRoleResponse(view))
}

// DeleteRole removes a custom role without members.
func (controller *AccessController) DeleteRole(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := identity(r)
	if !ok {
		writeAccessError(w, apiError.UnauthorizedAccess)
		return
	}
	if err := controller.Roles.DeleteRole(r.Context(), tenantID, chi.URLParam(r, "roleID")); err != nil {
		writeAccessError(w, err.Error())
		return
	}
	writeAccessSuccess(w, http.StatusOK, "Successfully deleted role.", nil)
}

// AssignCustomRole sets or clears the custom role of a USER account. Nobody
// assigns their own role: the caller was authorized as OWNER or ADMIN, and a
// target that is USER by the time of the write would be the caller demoted.
func (controller *AccessController) AssignCustomRole(w http.ResponseWriter, r *http.Request) {
	tenantID, actorID, ok := identity(r)
	if !ok {
		writeAccessError(w, apiError.UnauthorizedAccess)
		return
	}
	var request AssignCustomRoleRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, roleRequestMaxBytes)).Decode(&request); err != nil || request.CustomRoleID == nil {
		writeAccessError(w, apiError.InvalidPayload)
		return
	}
	targetID := strings.TrimSpace(chi.URLParam(r, "ID"))
	if targetID == "" {
		writeAccessError(w, apiError.InvalidPayload)
		return
	}
	if targetID == actorID {
		writeAccessError(w, apiError.ForbiddenAccess)
		return
	}
	if err := controller.Roles.AssignCustomRole(r.Context(), tenantID, targetID, *request.CustomRoleID); err != nil {
		writeAccessError(w, err.Error())
		return
	}
	writeAccessSuccess(w, http.StatusOK, "Successfully updated the member's role.", map[string]string{
		"userId": targetID, "customRoleId": strings.TrimSpace(*request.CustomRoleID),
	})
}

func writeAccessSuccess(w http.ResponseWriter, status int, message string, data interface{}) {
	response := viewmodels.HTTPResponseVM{Status: status, Success: true, Message: message, Data: data}
	response.JSON(w)
}

func writeAccessError(w http.ResponseWriter, code string) {
	status, message := http.StatusInternalServerError, "Please contact technical support."
	switch code {
	case apiError.InvalidPayload, apiError.InvalidRequestPayload:
		status, message, code = http.StatusBadRequest, "Invalid request.", apiError.InvalidPayload
	case apiError.UnauthorizedAccess:
		status, message = http.StatusUnauthorized, "Unauthorized access."
	case apiError.ForbiddenAccess:
		status, message = http.StatusForbidden, "Forbidden access."
	case apiError.MissingRecord:
		status, message = http.StatusNotFound, "No records found."
	case apiError.DuplicateRecord:
		status, message = http.StatusConflict, "A role with this name already exists."
	case apiError.RoleInUse:
		status, message = http.StatusConflict, "Members still hold this role."
	case apiError.AccessUnavailable:
		status, message = http.StatusServiceUnavailable, "Access verification is temporarily unavailable."
	case apiError.FirestoreError:
		message = "Firestore service encountered an error."
	default:
		code = "INTERNAL_ERROR"
	}
	response := viewmodels.HTTPResponseVM{Status: status, Success: false, Message: message, ErrorCode: code}
	response.JSON(w)
}
