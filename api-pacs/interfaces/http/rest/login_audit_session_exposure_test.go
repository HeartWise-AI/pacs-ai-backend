package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8/typedapi/cat/indices"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"
	estypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"
	"github.com/go-chi/chi/v5"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/require"

	esdb "api-pacs/infrastructures/database/elasticsearch/types"
	identitytypes "api-pacs/infrastructures/providers/api/identitytoolkit/types"
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	iammiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	iamtypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	accessdomain "api-pacs/module/access/domain/entity"
	accessrepo "api-pacs/module/access/domain/repository"
	accesssvc "api-pacs/module/access/infrastructure/service"
	accessrest "api-pacs/module/access/interfaces/http/rest"
	esentity "api-pacs/module/elasticsearch/domain/entity"
	esrepo "api-pacs/module/elasticsearch/infrastructure/repository"
	essvc "api-pacs/module/elasticsearch/infrastructure/service"
	ecsrest "api-pacs/module/elasticsearch/interfaces/http/rest"
	iamapp "api-pacs/module/iam/application"
	iamentity "api-pacs/module/iam/domain/entity"
	iamrepo "api-pacs/module/iam/infrastructure/repository"
	iamsvc "api-pacs/module/iam/infrastructure/service"
	iamsvctypes "api-pacs/module/iam/infrastructure/service/types"
	tenantapp "api-pacs/module/tenant/application"
	tenanttypes "api-pacs/module/tenant/infrastructure/service/types"
	userapp "api-pacs/module/user/application"
	usertypes "api-pacs/module/user/infrastructure/service/types"
)

const (
	loginAuditTenantID = "tenant-a"
	loginAuditOwnerID  = "owner-user"
	loginAuditAuditor  = "auditor-user"
	loginAuditPlain    = "plain-user"
)

// TestLogsViewCustomRoleCannotReadOrReplayLiveLoginSession follows the product
// login, custom-role, and logs routes. A logs.view user may read the login
// audit, and neither that response nor the indexed document may carry a bearer
// that authenticates as the logged-in account or extends its session.
func TestLogsViewCustomRoleCannotReadOrReplayLiveLoginSession(t *testing.T) {
	redis := newLoginAuditRedis()
	documents := newLoginAuditDocuments()
	iamCommand, iamQuery := newLoginAuditIAM(t, redis, documents)
	access := newLoginAuditAccess()
	router := newLoginAuditRouter(iamCommand, iamQuery, access, documents)

	ownerSession, err := iamCommand.LoginTenantUser(t.Context(), iamsvctypes.LoginTenantUser{
		TenantID: loginAuditTenantID,
		Email:    "owner@example.com",
		Password: "placeholder-password",
		ClientIP: "203.0.113.10",
	})
	require.NoError(t, err)
	require.NotEmpty(t, ownerSession)
	documents.waitForUser(t, loginAuditOwnerID)

	auditorSession := ksuid.New().String()
	plainSession := ksuid.New().String()
	require.NoError(t, iamCommand.SetTokenSession(t.Context(), iamsvctypes.SetTokenSession{
		SessionID: auditorSession, TenantID: loginAuditTenantID, UserID: loginAuditAuditor, Role: iamentity.UserRole,
	}))
	require.NoError(t, iamCommand.SetTokenSession(t.Context(), iamsvctypes.SetTokenSession{
		SessionID: plainSession, TenantID: loginAuditTenantID, UserID: loginAuditPlain, Role: iamentity.UserRole,
	}))

	roleID := createLogsViewRole(t, router, ownerSession)
	assignCustomRole(t, router, ownerSession, loginAuditAuditor, roleID)
	ownerExpiry, ok := redis.expiry(ownerSession)
	require.True(t, ok, "owner session was not stored")

	plainLogs := loginAuditGET(router, "/v1/ecs/logs?"+loginAuditQuery(false), plainSession)
	require.Equal(t, http.StatusForbidden, plainLogs.Code, "a user without logs.view must not read login logs")
	requireOmitsLiveSession(t, "forbidden login log response", plainLogs.Body.String(), ownerSession)

	jsonLogs := loginAuditGET(router, "/v1/ecs/logs?"+loginAuditQuery(false), auditorSession)
	require.Equal(t, http.StatusOK, jsonLogs.Code, "logs.view must still be able to read the login audit")
	require.Contains(t, jsonLogs.Body.String(), loginAuditOwnerID)
	require.Contains(t, jsonLogs.Body.String(), iamentity.OwnerRole)
	requireOmitsLiveSession(t, "login log JSON", jsonLogs.Body.String(), ownerSession)

	csvLogs := loginAuditGET(router, "/v1/ecs/logs?"+loginAuditQuery(true), auditorSession)
	require.Equal(t, http.StatusOK, csvLogs.Code)
	require.Contains(t, csvLogs.Body.String(), loginAuditOwnerID)
	requireOmitsLiveSession(t, "login log CSV", csvLogs.Body.String(), ownerSession)

	for _, document := range documents.loginDocuments() {
		requireOmitsLiveSession(t, "indexed login audit", string(document), ownerSession)
	}

	seen := map[string]struct{}{}
	for _, candidate := range append(jsonStrings(t, jsonLogs.Body.Bytes()), csvFields(csvLogs.Body.String())...) {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		for _, path := range []string{"/v1/probe/whoami", "/v1/proxy-probe/whoami"} {
			status, userID, role := loginAuditWho(router, path, candidate)
			if status == http.StatusOK && userID == loginAuditOwnerID && role == iamentity.OwnerRole {
				t.Errorf("a value returned by the login logs authenticated as the owner on %s", path)
			}
		}
	}

	refreshed, ok := redis.expiry(ownerSession)
	require.True(t, ok, "owner session disappeared")
	if !refreshed.Equal(ownerExpiry) {
		t.Errorf("replaying a login-log value extended the owner session")
	}
}

func createLogsViewRole(t *testing.T, router http.Handler, ownerSession string) string {
	t.Helper()
	body := `{"name":"Auditor","description":"Login audit","permissions":["logs.view"],"allModels":true}`
	request := httptest.NewRequest(http.MethodPost, "/v1/user/roles", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+ownerSession)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var created struct {
		Data struct {
			ID          string   `json:"id"`
			Permissions []string `json:"permissions"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
	require.Equal(t, []string{accessdomain.PermLogsView}, created.Data.Permissions)
	require.NotEmpty(t, created.Data.ID)
	return created.Data.ID
}

func assignCustomRole(t *testing.T, router http.Handler, ownerSession, userID, roleID string) {
	t.Helper()
	body := `{"customRoleId":"` + roleID + `"}`
	request := httptest.NewRequest(http.MethodPut, "/v1/user/"+userID+"/custom-role", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+ownerSession)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

func loginAuditQuery(export bool) string {
	day := time.Now().UTC().Format("2006-01-02")
	query := "index=" + (&esentity.Login{}).GetModelName() + "&startDate=" + day + "&endDate=" + day
	if export {
		return query + "&export=true"
	}
	return query
}

func loginAuditGET(router http.Handler, target, session string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Authorization", "Bearer "+session)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func loginAuditWho(router http.Handler, target, session string) (int, string, string) {
	recorder := loginAuditGET(router, target, session)
	var body struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	return recorder.Code, body.UserID, body.Role
}

func requireOmitsLiveSession(t *testing.T, label, body, session string) {
	t.Helper()
	if session != "" && strings.Contains(body, session) {
		t.Errorf("%s contains the live session token", label)
	}
}

func jsonStrings(t *testing.T, body []byte) []string {
	t.Helper()
	var value any
	require.NoError(t, json.Unmarshal(body, &value))
	var found []string
	collectJSONStrings(value, &found)
	return found
}

func collectJSONStrings(value any, found *[]string) {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			*found = append(*found, typed)
		}
	case []any:
		for _, item := range typed {
			collectJSONStrings(item, found)
		}
	case map[string]any:
		for _, item := range typed {
			collectJSONStrings(item, found)
		}
	}
}

func csvFields(body string) []string {
	var found []string
	for _, line := range strings.Split(body, "\n") {
		for _, field := range strings.Split(line, ",") {
			field = strings.Trim(strings.TrimSpace(field), `"`)
			if field != "" {
				found = append(found, field)
			}
		}
	}
	return found
}

func newLoginAuditRouter(command *iamsvc.IAMCommandService, query *iamsvc.IAMQueryService, access *accesssvc.AccessService, documents *loginAuditDocuments) http.Handler {
	middleware := &iammiddleware.IAMMiddleware{
		IAMCommandServiceInterface: command,
		IAMQueryServiceInterface:   query,
		AccessResolver:             access,
	}
	logs := &ecsrest.ElasticsearchQueryController{ElasticsearchQueryServiceInterface: &essvc.ElasticsearchQueryService{
		ElasticsearchQueryRepositoryInterface: &esrepo.ElasticsearchQueryRepository{ElasticsearchDBHandlerInterface: documents},
	}}
	roles := &accessrest.AccessController{Roles: access}
	who := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"userId": r.Context().Value(iamtypes.UserIDCtx).(string),
			"role":   r.Context().Value(iamtypes.RoleCtx).(string),
		})
	}

	router := chi.NewRouter()
	router.Route("/v1", func(r chi.Router) {
		r.Route("/ecs", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(middleware.TokenSessionAuthGuard)
				r.Use(middleware.RequirePermission(accessdomain.PermLogsView))
				r.Get("/logs", logs.SearchDocumentLogs)
			})
		})
		r.Route("/user", func(r chi.Router) {
			r.Use(middleware.TokenSessionAuthGuard)
			mountUserMemberRoutes(r, userMemberGuards{
				invite:       middleware.RequirePermission(accessdomain.PermMembersInvite),
				manage:       middleware.RequirePermission(accessdomain.PermMembersManage),
				ownerOrAdmin: middleware.RBACOwnerOrAdminGuard,
			}, userMemberHandlers{
				createRole: roles.CreateRole,
				updateRole: roles.UpdateRole,
				deleteRole: roles.DeleteRole,
				assignRole: roles.AssignCustomRole,
			})
		})
		r.Route("/probe", func(r chi.Router) {
			r.Use(middleware.TokenSessionAuthGuard)
			r.Get("/whoami", who)
		})
		r.Route("/proxy-probe", func(r chi.Router) {
			r.Use(middleware.TokenSessionOrthancProxyAuthGuard)
			r.Get("/whoami", who)
		})
	})
	return router
}

func newLoginAuditIAM(t *testing.T, redis *loginAuditRedis, documents *loginAuditDocuments) (*iamsvc.IAMCommandService, *iamsvc.IAMQueryService) {
	t.Helper()
	store := &iamrepo.IAMCommandRepository{RedisDBHandlerInterface: redis}
	command := &iamsvc.IAMCommandService{
		IAMCommandRepositoryInterface: store,
		UserQueryServiceInterface: &loginAuditUsers{user: usertypes.GetTenantUser{
			ID: loginAuditOwnerID, TenantID: loginAuditTenantID, Email: "owner@example.com",
			Name: "Owner Example", Role: iamentity.OwnerRole, Specialty: "cardiology",
			AccessState: "ACTIVE", IsEmailVerified: true,
		}},
		ElasticsearchCommandServiceInterface: &essvc.ElasticsearchCommandService{
			ElasticsearchCommandRepositoryInterface: &esrepo.ElasticsearchCommandRepository{ElasticsearchDBHandlerInterface: documents},
		},
		TenantQueryServiceInterface:    &loginAuditTenants{name: "Example Workspace"},
		TenantIDTokenVerifierInterface: loginAuditVerifier{userID: loginAuditOwnerID},
		IdentityToolkitAPIInterface: loginAuditIdentity{response: identitytypes.SignInWithPasswordResponse{
			IDToken: "placeholder-id-token", LocalID: loginAuditOwnerID, Email: "owner@example.com",
		}},
		LoginAbuseProtectionInterface: loginAuditProtection{},
	}
	query := &iamsvc.IAMQueryService{IAMQueryRepositoryInterface: &iamrepo.IAMQueryRepository{RedisDBHandlerInterface: redis}}
	return command, query
}

func newLoginAuditAccess() *accesssvc.AccessService {
	repository := &loginAuditRoles{roles: map[string]accessdomain.CustomRole{}, users: map[string]accessrepo.UserRoleAssignment{
		loginAuditOwnerID: {UserID: loginAuditOwnerID, TenantID: loginAuditTenantID, Role: iamentity.OwnerRole},
		loginAuditAuditor: {UserID: loginAuditAuditor, TenantID: loginAuditTenantID, Role: iamentity.UserRole},
		loginAuditPlain:   {UserID: loginAuditPlain, TenantID: loginAuditTenantID, Role: iamentity.UserRole},
	}}
	return &accesssvc.AccessService{Repository: repository, TTL: time.Second}
}

type loginAuditUsers struct {
	userapp.UserQueryServiceInterface
	user usertypes.GetTenantUser
}

func (query *loginAuditUsers) GetTenantUserByID(context.Context, string, string) (usertypes.GetTenantUser, error) {
	return query.user, nil
}

type loginAuditTenants struct {
	tenantapp.TenantQueryServiceInterface
	name string
}

func (query *loginAuditTenants) GetTenantByID(context.Context, string) (tenanttypes.GetTenantResult, error) {
	return tenanttypes.GetTenantResult{ID: loginAuditTenantID, Name: query.name}, nil
}

type loginAuditVerifier struct {
	firebaseadmin.TenantIDTokenVerifierInterface
	userID string
}

func (verifier loginAuditVerifier) VerifyTenantIDToken(context.Context, string, string) (string, error) {
	return verifier.userID, nil
}

type loginAuditIdentity struct {
	identitytypes.IdentityToolkitAPIInterface
	response identitytypes.SignInWithPasswordResponse
}

func (api loginAuditIdentity) SignInWithPassword(context.Context, identitytypes.SignInWithPasswordRequest) (identitytypes.SignInWithPasswordResponse, error) {
	return api.response, nil
}

type loginAuditProtection struct{}

func (loginAuditProtection) EvaluateLoginAttempt(context.Context, iamsvctypes.LoginAbuseSignals) (iamsvctypes.LoginProtectionDecision, error) {
	return iamsvctypes.LoginProtectionDecision{}, nil
}

func (loginAuditProtection) RecordLoginFailure(context.Context, iamsvctypes.LoginAbuseSignals) (iamsvctypes.LoginProtectionDecision, error) {
	return iamsvctypes.LoginProtectionDecision{}, nil
}

func (loginAuditProtection) ResetAccountFailures(context.Context, iamsvctypes.LoginAbuseSignals) error {
	return nil
}

// loginAuditDocuments stores the JSON encoding Index sends and returns it as
// _source. Search keeps a document when tenant_id equals the multi_match value
// and timestamp falls in the range ElasticsearchDBHandler requests; the search
// request does not set _source, so every stored field is returned.
type loginAuditDocuments struct {
	mu   sync.Mutex
	docs map[string][][]byte
}

func newLoginAuditDocuments() *loginAuditDocuments {
	return &loginAuditDocuments{docs: map[string][][]byte{}}
}

func (store *loginAuditDocuments) GetAllIndices() (indices.Response, error) {
	return nil, nil
}

func (store *loginAuditDocuments) IndexDocument(_ context.Context, indexName string, document interface{}) (*index.Response, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	store.mu.Lock()
	store.docs[indexName] = append(store.docs[indexName], encoded)
	store.mu.Unlock()
	return &index.Response{}, nil
}

func (store *loginAuditDocuments) SearchDocuments(_ context.Context, param esdb.SearchDocument) (*search.Response, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	hits := []estypes.Hit{}
	for _, document := range store.docs[param.Index] {
		var fields map[string]any
		if err := json.Unmarshal(document, &fields); err != nil {
			return nil, err
		}
		tenantID, _ := fields["tenant_id"].(string)
		if tenantID != param.TenantID {
			continue
		}
		timestamp, ok := fields["timestamp"].(float64)
		if !ok || timestamp < float64(param.StartDate) || timestamp > float64(param.EndDate) {
			continue
		}
		if param.Query != "" && !strings.Contains(string(document), param.Query) {
			continue
		}
		copied := append([]byte(nil), document...)
		hits = append(hits, estypes.Hit{Source_: copied})
	}
	return &search.Response{Hits: estypes.HitsMetadata{Hits: hits}}, nil
}

func (store *loginAuditDocuments) loginDocuments() [][]byte {
	store.mu.Lock()
	defer store.mu.Unlock()
	copies := make([][]byte, len(store.docs["logins"]))
	for i, document := range store.docs["logins"] {
		copies[i] = append([]byte(nil), document...)
	}
	return copies
}

func (store *loginAuditDocuments) waitForUser(t *testing.T, userID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, document := range store.loginDocuments() {
			if strings.Contains(string(document), userID) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("successful login did not write a login audit document")
}

// loginAuditRedis applies the SET PX script in RedisDBHandler: the session key
// is written whenever the suspension key is absent, including when the session
// key already exists, and the expiry argument replaces the previous TTL.
type loginAuditRedis struct {
	mu        sync.Mutex
	values    map[string]string
	deadlines map[string]time.Time
}

func newLoginAuditRedis() *loginAuditRedis {
	return &loginAuditRedis{values: map[string]string{}, deadlines: map[string]time.Time{}}
}

func (redis *loginAuditRedis) expiry(key string) (time.Time, bool) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	if !redis.alive(key, time.Now()) {
		return time.Time{}, false
	}
	return redis.deadlines[key], true
}

func (redis *loginAuditRedis) alive(key string, now time.Time) bool {
	deadline, ok := redis.deadlines[key]
	if !ok {
		return false
	}
	if !now.Before(deadline) {
		delete(redis.values, key)
		delete(redis.deadlines, key)
		return false
	}
	return true
}

func (redis *loginAuditRedis) Get(key string) (string, error) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	if !redis.alive(key, time.Now()) {
		return "", errors.New("empty")
	}
	return redis.values[key], nil
}

func (redis *loginAuditRedis) Set(key string, val interface{}, exp time.Duration) error {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	redis.values[key] = loginAuditRedisArg(val)
	redis.deadlines[key] = time.Now().Add(exp)
	return nil
}

func (redis *loginAuditRedis) SetIfAbsent(key string, val interface{}, exp time.Duration) (bool, error) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	if redis.alive(key, time.Now()) {
		return false, nil
	}
	redis.values[key] = loginAuditRedisArg(val)
	redis.deadlines[key] = time.Now().Add(exp)
	return true, nil
}

func (redis *loginAuditRedis) SetIfKeyAbsent(blockingKey, key string, val interface{}, exp time.Duration) (bool, error) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	now := time.Now()
	if redis.alive(blockingKey, now) {
		return false, nil
	}
	redis.values[key] = loginAuditRedisArg(val)
	redis.deadlines[key] = now.Add(exp)
	return true, nil
}

func (redis *loginAuditRedis) Delete(key string) error {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	delete(redis.values, key)
	delete(redis.deadlines, key)
	return nil
}

func (redis *loginAuditRedis) DeleteIfValueMatches(key string, val interface{}) (bool, error) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	if !redis.alive(key, time.Now()) || redis.values[key] != loginAuditRedisArg(val) {
		return false, nil
	}
	delete(redis.values, key)
	delete(redis.deadlines, key)
	return true, nil
}

func (redis *loginAuditRedis) Flush() {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	redis.values = map[string]string{}
	redis.deadlines = map[string]time.Time{}
}

func (redis *loginAuditRedis) Scan(string) ([]string, error) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	keys := make([]string, 0, len(redis.values))
	now := time.Now()
	for key := range redis.values {
		if redis.alive(key, now) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (redis *loginAuditRedis) GetCounterWithExpiry(string) (int64, time.Duration, error) {
	return 0, 0, nil
}

func (redis *loginAuditRedis) IncrementWithExpiry(string, time.Duration) (int64, time.Duration, error) {
	return 0, 0, nil
}

func loginAuditRedisArg(val interface{}) string {
	switch typed := val.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

type loginAuditRoles struct {
	mu    sync.Mutex
	roles map[string]accessdomain.CustomRole
	users map[string]accessrepo.UserRoleAssignment
}

func (repository *loginAuditRoles) ListRoles(context.Context, string) ([]accessdomain.CustomRole, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	roles := []accessdomain.CustomRole{}
	for _, role := range repository.roles {
		if role.TenantID == loginAuditTenantID {
			roles = append(roles, role)
		}
	}
	return roles, nil
}

func (repository *loginAuditRoles) GetRole(_ context.Context, tenantID, roleID string) (accessdomain.CustomRole, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	role, ok := repository.roles[roleID]
	if !ok || role.TenantID != tenantID {
		return accessdomain.CustomRole{}, errors.New(apiError.MissingRecord)
	}
	return role, nil
}

func (repository *loginAuditRoles) CreateRole(_ context.Context, role accessdomain.CustomRole) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.roles[role.ID] = role
	return nil
}

func (repository *loginAuditRoles) UpdateRole(_ context.Context, role accessdomain.CustomRole) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, ok := repository.roles[role.ID]; !ok {
		return errors.New(apiError.MissingRecord)
	}
	repository.roles[role.ID] = role
	return nil
}

func (repository *loginAuditRoles) DeleteRole(_ context.Context, tenantID, roleID string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, user := range repository.users {
		if user.TenantID == tenantID && user.CustomRoleID == roleID {
			return errors.New(apiError.RoleInUse)
		}
	}
	delete(repository.roles, roleID)
	return nil
}

func (repository *loginAuditRoles) GetUserAssignment(_ context.Context, tenantID, userID string) (accessrepo.UserRoleAssignment, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	user, ok := repository.users[userID]
	if !ok || user.TenantID != tenantID {
		return accessrepo.UserRoleAssignment{}, errors.New(apiError.MissingRecord)
	}
	return user, nil
}

func (repository *loginAuditRoles) ListUserAssignments(context.Context, string) ([]accessrepo.UserRoleAssignment, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	users := []accessrepo.UserRoleAssignment{}
	for _, user := range repository.users {
		if user.TenantID == loginAuditTenantID {
			users = append(users, user)
		}
	}
	return users, nil
}

func (repository *loginAuditRoles) SetUserCustomRole(_ context.Context, tenantID, userID, roleID string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	user, ok := repository.users[userID]
	if !ok || user.TenantID != tenantID {
		return errors.New(apiError.MissingRecord)
	}
	user.CustomRoleID = roleID
	repository.users[userID] = user
	return nil
}

var (
	_ esdb.ElasticsearchDBHandlerInterface = (*loginAuditDocuments)(nil)
	_ iamapp.LoginAbuseProtectionInterface = loginAuditProtection{}
)
