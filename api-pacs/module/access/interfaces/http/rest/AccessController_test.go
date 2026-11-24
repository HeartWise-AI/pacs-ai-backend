package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/accessctx"
	accessEntity "api-pacs/module/access/domain/entity"
	accessService "api-pacs/module/access/infrastructure/service"
	iamEntity "api-pacs/module/iam/domain/entity"
)

type stubRoles struct {
	assigned [3]string
	input    accessService.RoleInput
	err      error
}

func (roles *stubRoles) ListRoles(context.Context, string) ([]accessService.RoleView, error) {
	return []accessService.RoleView{{CustomRole: accessEntity.CustomRole{ID: "r1", Name: "Readers"}, MemberCount: 2}}, roles.err
}

func (roles *stubRoles) CreateRole(_ context.Context, _, _ string, input accessService.RoleInput) (accessService.RoleView, error) {
	roles.input = input
	return accessService.RoleView{CustomRole: accessEntity.CustomRole{ID: "new", Name: input.Name}}, roles.err
}

func (roles *stubRoles) UpdateRole(_ context.Context, _, _ string, input accessService.RoleInput) (accessService.RoleView, error) {
	roles.input = input
	return accessService.RoleView{}, roles.err
}

func (roles *stubRoles) DeleteRole(context.Context, string, string) error { return roles.err }

func (roles *stubRoles) AssignCustomRole(_ context.Context, tenantID, userID, roleID string) error {
	roles.assigned = [3]string{tenantID, userID, roleID}
	return roles.err
}

func signedIn(request *http.Request, access *accessEntity.Access) *http.Request {
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "t1")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "u1")
	if access != nil {
		ctx = accessctx.WithAccess(ctx, *access)
	}
	return request.WithContext(ctx)
}

type envelope struct {
	Data      json.RawMessage `json:"data"`
	ErrorCode string          `json:"errorCode"`
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) envelope {
	t.Helper()
	var body envelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func TestGetMyAccessShape(t *testing.T) {
	access := accessEntity.ForRole(iamEntity.UserRole, &accessEntity.CustomRole{ID: "r1", Name: "Readers", Permissions: []string{accessEntity.PermFeedbackView}, ModelIDs: []string{"PanEcho"}})
	recorder := httptest.NewRecorder()
	(&AccessController{}).GetMyAccess(recorder, signedIn(httptest.NewRequest(http.MethodGet, "/", nil), &access))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"role":"USER","permissions":["models.use","feedback.vote","feedback.view"],"allModels":false,"modelIds":["PanEcho"],"customRole":{"id":"r1","name":"Readers"}}`, string(decode(t, recorder).Data))

	admin := accessEntity.ForRole(iamEntity.AdminRole, nil)
	recorder = httptest.NewRecorder()
	(&AccessController{}).GetMyAccess(recorder, signedIn(httptest.NewRequest(http.MethodGet, "/", nil), &admin))
	var body AccessResponse
	require.NoError(t, json.Unmarshal(decode(t, recorder).Data, &body))
	require.Equal(t, accessEntity.AllPermissions(), body.Permissions)
	require.True(t, body.AllModels)
	require.Equal(t, []string{}, body.ModelIDs)
	require.Nil(t, body.CustomRole)

	recorder = httptest.NewRecorder()
	(&AccessController{}).GetMyAccess(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestListRolesIncludesCatalog(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&AccessController{Roles: &stubRoles{}}).ListRoles(recorder, signedIn(httptest.NewRequest(http.MethodGet, "/", nil), nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var body RoleListResponse
	require.NoError(t, json.Unmarshal(decode(t, recorder).Data, &body))
	require.Equal(t, "r1", body.Roles[0].ID)
	require.Equal(t, 2, body.Roles[0].MemberCount)
	require.Equal(t, []string{}, body.Roles[0].Permissions)
	require.Len(t, body.Permissions, len(accessEntity.AllPermissions()))
}

func TestCreateRoleDecodesBody(t *testing.T) {
	roles := &stubRoles{}
	recorder := httptest.NewRecorder()
	body := `{"name":"Readers","description":"d","permissions":["feedback.view"],"allModels":false,"modelIds":["PanEcho"]}`
	(&AccessController{Roles: roles}).CreateRole(recorder, signedIn(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), nil))
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, accessService.RoleInput{Name: "Readers", Description: "d", Permissions: []string{"feedback.view"}, ModelIDs: []string{"PanEcho"}}, roles.input)

	recorder = httptest.NewRecorder()
	(&AccessController{Roles: roles}).CreateRole(recorder, signedIn(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{")), nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestAssignCustomRole(t *testing.T) {
	route := func(roles *stubRoles, body string) *httptest.ResponseRecorder {
		router := chi.NewRouter()
		controller := &AccessController{Roles: roles}
		router.Put("/user/{ID}/custom-role", func(w http.ResponseWriter, r *http.Request) {
			controller.AssignCustomRole(w, signedIn(r, nil))
		})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/user/u2/custom-role", strings.NewReader(body)))
		return recorder
	}
	roles := &stubRoles{}
	require.Equal(t, http.StatusOK, route(roles, `{"customRoleId":"r1"}`).Code)
	require.Equal(t, [3]string{"t1", "u2", "r1"}, roles.assigned)
	require.Equal(t, http.StatusOK, route(roles, `{"customRoleId":""}`).Code)
	require.Equal(t, "", roles.assigned[2])
	require.Equal(t, http.StatusBadRequest, route(roles, `{}`).Code, "customRoleId is required, even when clearing")

	for code, status := range map[string]int{
		apiError.InvalidPayload: http.StatusBadRequest, apiError.MissingRecord: http.StatusNotFound,
		apiError.RoleInUse: http.StatusConflict, apiError.DuplicateRecord: http.StatusConflict,
		apiError.AccessUnavailable: http.StatusServiceUnavailable, apiError.FirestoreError: http.StatusInternalServerError,
	} {
		recorder := route(&stubRoles{err: errors.New(code)}, `{"customRoleId":"r1"}`)
		require.Equal(t, status, recorder.Code, code)
		require.Equal(t, code, decode(t, recorder).ErrorCode)
	}
}
