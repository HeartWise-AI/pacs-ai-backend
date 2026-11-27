package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	esindex "github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	esTypes "api-pacs/infrastructures/database/elasticsearch/types"
	redisTypes "api-pacs/infrastructures/database/redis/types"
	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	esEntity "api-pacs/module/elasticsearch/domain/entity"
	esRepository "api-pacs/module/elasticsearch/infrastructure/repository"
	esService "api-pacs/module/elasticsearch/infrastructure/service"
	elasticsearchHTTP "api-pacs/module/elasticsearch/interfaces/http"
	iamEntity "api-pacs/module/iam/domain/entity"
	iamRepository "api-pacs/module/iam/infrastructure/repository"
	tenantApplication "api-pacs/module/tenant/application"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
	userRest "api-pacs/module/user/interfaces/http/rest"
)

func TestLoginAuditRecordIsNotAReusableBearer(t *testing.T) {
	sessions := newScriptRedis()
	commandRepository := &iamRepository.IAMCommandRepository{RedisDBHandlerInterface: sessions}
	queryRepository := &iamRepository.IAMQueryRepository{RedisDBHandlerInterface: sessions}
	captured := make(chan esEntity.Login, 1)
	elasticsearch := &esService.ElasticsearchCommandService{
		ElasticsearchCommandRepositoryInterface: &esRepository.ElasticsearchCommandRepository{
			ElasticsearchDBHandlerInterface: loginCapture{login: captured},
		},
	}

	iamService, _, identityAPI, verifier, _ := newLoginTestService()
	iamService.IAMCommandRepositoryInterface = commandRepository
	iamService.ElasticsearchCommandServiceInterface = elasticsearch
	iamService.TenantQueryServiceInterface = tenantName{name: "Example Workspace"}
	owner := iamService.UserQueryServiceInterface.(*loginTestUserQuery)
	owner.user.Role = iamEntity.OwnerRole
	owner.user.Name = "Owner Example"
	owner.user.Specialty = "cardiology"
	identityAPI.response.LocalID = owner.user.ID
	verifier.userID = owner.user.ID

	sessionToken, err := iamService.LoginTenantUser(t.Context(), testLoginInput())
	require.NoError(t, err)
	require.NotEmpty(t, sessionToken)

	var login esEntity.Login
	select {
	case login = <-captured:
	case <-time.After(2 * time.Second):
		t.Fatal("login did not write an audit record")
	}
	require.Equal(t, "logins", login.GetModelName())

	queryService := &IAMQueryService{IAMQueryRepositoryInterface: queryRepository}
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: iamService,
		IAMQueryServiceInterface:   queryService,
		UserQueryServiceInterface:  owner,
	}
	currentUser := (&userRest.UserQueryController{UserQueryServiceInterface: owner}).GetCurrentTenantUser
	userRouter := chi.NewRouter()
	userRouter.Route("/v1/user", func(r chi.Router) {
		r.Use(middleware.TokenSessionAuthGuard)
		r.Get("/me", currentUser)
	})
	proxyRouter := chi.NewRouter()
	proxyRouter.With(middleware.TokenSessionOrthancProxyAuthGuard).Get("/proxy/orthanc/dicom-web/studies", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	api := apiPrefix(userRouter)
	proxy := apiPrefix(proxyRouter)

	require.Equal(t, 1, sessions.sets)
	me := requestWithBearer(api, "/api/v1/user/me", sessionToken)
	require.Equal(t, http.StatusOK, me.Code)
	require.Contains(t, me.Body.String(), owner.user.Email)
	require.Equal(t, 2, sessions.sets)
	require.Equal(t, time.Duration(iamEntity.ExpireTimeInSeconds)*time.Second, sessions.ttl(sessionToken))

	orthanc := requestWithBearer(proxy, "/api/proxy/orthanc/dicom-web/studies", sessionToken)
	require.Equal(t, http.StatusNoContent, orthanc.Code)

	for _, candidate := range append(jsonStringValues(t, login), jsonStringValues(t, loginLogResponse(login))...) {
		meStatus := requestWithBearer(api, "/api/v1/user/me", candidate)
		proxyStatus := requestWithBearer(proxy, "/api/proxy/orthanc/dicom-web/studies", candidate)
		require.NotEqual(t, http.StatusOK, meStatus.Code, "audit value authenticated /api/v1/user/me")
		require.NotEqual(t, http.StatusNoContent, proxyStatus.Code, "audit value authenticated the orthanc proxy")
	}
}

func loginLogResponse(login esEntity.Login) elasticsearchHTTP.LoginLogResponse {
	return elasticsearchHTTP.LoginLogResponse{
		SessionID: login.SessionID, TenantID: login.TenantID, TenantName: login.TenantName,
		UserID: login.UserID, Email: login.Email, Name: login.Name, Role: login.Role,
		Specialty: login.Specialty, Timestamp: login.Timestamp,
	}
}

type loginCapture struct {
	esTypes.ElasticsearchDBHandlerInterface
	login chan esEntity.Login
}

func (capture loginCapture) IndexDocument(_ context.Context, _ string, document interface{}) (*esindex.Response, error) {
	login, ok := document.(esEntity.Login)
	if !ok {
		return nil, errors.New("unexpected audit document")
	}
	capture.login <- login
	return &esindex.Response{}, nil
}

type tenantName struct {
	tenantApplication.TenantQueryServiceInterface
	name string
}

func (tenant tenantName) GetTenantByID(context.Context, string) (tenantTypes.GetTenantResult, error) {
	return tenantTypes.GetTenantResult{Name: tenant.name}, nil
}

type scriptRedis struct {
	redisTypes.RedisDBHandlerInterface
	mu   sync.Mutex
	keys map[string]string
	ttls map[string]time.Duration
	sets int
}

func newScriptRedis() *scriptRedis {
	return &scriptRedis{keys: map[string]string{}, ttls: map[string]time.Duration{}}
}

func (redis *scriptRedis) Get(key string) (string, error) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	value, ok := redis.keys[key]
	if !ok {
		return "", errors.New("empty")
	}
	return value, nil
}

func (redis *scriptRedis) SetIfKeyAbsent(blockingKey, key string, val interface{}, exp time.Duration) (bool, error) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	if _, blocked := redis.keys[blockingKey]; blocked {
		return false, nil
	}
	switch typed := val.(type) {
	case []byte:
		redis.keys[key] = string(typed)
	case string:
		redis.keys[key] = typed
	default:
		redis.keys[key] = fmt.Sprint(typed)
	}
	redis.ttls[key] = exp
	redis.sets++
	return true, nil
}

func (redis *scriptRedis) ttl(key string) time.Duration {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	return redis.ttls[key]
}

var apiPrefixPattern = regexp.MustCompile(`^/api(/.*)$`)

func apiPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if match := apiPrefixPattern.FindStringSubmatch(r.URL.Path); match != nil {
			r.URL.Path = match[1]
			if r.URL.RawPath != "" {
				r.URL.RawPath = ""
			}
		}
		next.ServeHTTP(w, r)
	})
}

func requestWithBearer(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func jsonStringValues(t *testing.T, value any) []string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	var decoded any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	var values []string
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case string:
			if typed != "" {
				values = append(values, typed)
			}
		case map[string]any:
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(decoded)
	return values
}
