package elasticsearch_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	esdb "api-pacs/infrastructures/database/elasticsearch"
	estypes "api-pacs/infrastructures/database/elasticsearch/types"
	iammiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	apiError "api-pacs/internal/errors"
	accessEntity "api-pacs/module/access/domain/entity"
	esentity "api-pacs/module/elasticsearch/domain/entity"
	esrepository "api-pacs/module/elasticsearch/infrastructure/repository"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
	esservice "api-pacs/module/elasticsearch/infrastructure/service"
	esrest "api-pacs/module/elasticsearch/interfaces/http/rest"
	iamapplication "api-pacs/module/iam/application"
	iamentity "api-pacs/module/iam/domain/entity"
	iamservice "api-pacs/module/iam/infrastructure/service/types"
)

const (
	callerTenant   = "pacs-mhi-aaaa"
	victimTenant   = "pacs-chum-bbbb"
	permutedTenant = "aaaa-mhi-pacs"
	disjointTenant = "clinic-zzqq-9999"
	callerSession  = "caller-session-placeholder"
)

func TestLogsViewSearchReturnsOnlyTheExactCallerTenant(t *testing.T) {
	esURL := os.Getenv("ELASTICSEARCH_URL")
	if esURL == "" {
		esURL = "http://127.0.0.1:9200"
	}
	requireElasticsearch871(t, esURL)

	handler, err := esdb.NewTypedClient(estypes.Config{ElasticsearchURL: esURL})
	require.NoError(t, err)

	commandRepo := &esrepository.ElasticsearchCommandRepository{ElasticsearchDBHandlerInterface: handler}
	queryRepo := &esrepository.ElasticsearchQueryRepository{ElasticsearchDBHandlerInterface: handler}
	queryService := &esservice.ElasticsearchQueryService{
		ElasticsearchQueryRepositoryInterface: &esrepository.ElasticsearchQueryRepositoryCircuitBreaker{
			ElasticsearchQueryRepositoryInterface: queryRepo,
		},
	}
	controller := esrest.ElasticsearchQueryController{ElasticsearchQueryServiceInterface: queryService}
	logs := logsRouter(&controller, callerTenant, true)

	ctx := context.Background()
	indices := auditIndices()
	t.Cleanup(func() {
		for _, index := range indices {
			_, _ = handler.TypedClient.Indices.Delete(index).IgnoreUnavailable(true).Do(context.Background())
		}
	})
	for _, index := range indices {
		_, err := handler.TypedClient.Indices.Delete(index).IgnoreUnavailable(true).Do(ctx)
		require.NoError(t, err)
	}

	seedAuditLogs(t, ctx, commandRepo)
	_, err = handler.TypedClient.Indices.Refresh().Index(strings.Join(indices, ",")).Do(ctx)
	require.NoError(t, err)

	day := time.Now().UTC().Format("2006-01-02")
	for _, index := range indices {
		t.Run(index, func(t *testing.T) {
			body := getLogs(t, logs, index, day, "")
			assertExactCallerTenant(t, body, index)
		})
		t.Run(index+"/shared-text", func(t *testing.T) {
			body := getLogs(t, logs, index, day, "example")
			assertExactCallerTenant(t, body, index)
		})
		t.Run(index+"/other-tenant-text", func(t *testing.T) {
			body := getLogs(t, logs, index, day, marker(index, "victim"))
			assertNoOtherTenant(t, body, index)
		})
	}

	t.Run("logins/export", func(t *testing.T) {
		body := getLogsExport(t, logs, (&esentity.Login{}).GetModelName(), day)
		assertExactCallerTenant(t, body, (&esentity.Login{}).GetModelName())
	})
}

func TestLogsRouteRejectsCallerWithoutLogsView(t *testing.T) {
	controller := esrest.ElasticsearchQueryController{}
	logs := logsRouter(&controller, callerTenant, false)
	request := httptest.NewRequest(http.MethodGet, "/v1/ecs/logs?index=logins&startDate=2026-11-27&endDate=2026-11-27", nil)
	request.Header.Set("Authorization", "Bearer "+callerSession)
	recorder := httptest.NewRecorder()

	logs.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.NotContains(t, recorder.Body.String(), marker("logins", "victim"))
}

type party struct {
	tenantID string
	kind     string
}

func parties() []party {
	return []party{
		{callerTenant, "own"},
		{victimTenant, "victim"},
		{permutedTenant, "permuted"},
		{disjointTenant, "disjoint"},
	}
}

func marker(index, kind string) string {
	return kind + index
}

func seedAuditLogs(t *testing.T, ctx context.Context, repo *esrepository.ElasticsearchCommandRepository) {
	t.Helper()
	for _, index := range auditIndices() {
		for _, party := range parties() {
			seedParty(t, ctx, repo, index, party)
		}
	}
}

func seedParty(t *testing.T, ctx context.Context, repo *esrepository.ElasticsearchCommandRepository, index string, party party) {
	t.Helper()
	email := marker(index, party.kind) + "@example.test"
	name := party.kind
	var err error
	switch index {
	case (&esentity.AdminInvite{}).GetModelName():
		_, err = repo.InsertAdminInviteLog(ctx, repositoryTypes.CreateAdminInviteLog{
			TenantID: party.tenantID, TenantName: party.tenantID, Email: email,
		})
	case (&esentity.AdminMember{}).GetModelName():
		_, err = repo.InsertAdminMemberLog(ctx, repositoryTypes.CreateAdminMemberLog{
			TenantID: party.tenantID, TenantName: party.tenantID, UserID: marker(index, party.kind),
			Email: email, Name: name, Role: "USER", Action: esentity.CreateAction,
		})
	case (&esentity.Login{}).GetModelName():
		_, err = repo.InsertLoginLog(ctx, repositoryTypes.CreateLoginLog{
			SessionID: "session-" + marker(index, party.kind), TenantID: party.tenantID, TenantName: party.tenantID,
			UserID: marker(index, party.kind), Email: email, Name: name, Role: "USER",
		})
	case (&esentity.ModalityStudy{}).GetModelName():
		_, err = repo.InsertGetModalityStudyLog(ctx, repositoryTypes.CreateGetModalityStudyLog{
			TenantID: party.tenantID, TenantName: party.tenantID, ModalityID: "modality",
			UserID: marker(index, party.kind), Email: email, Name: name, QueryID: marker(index, party.kind),
		})
	case (&esentity.PredictInferenceModel{}).GetModelName():
		_, err = repo.InsertPredictInferenceModelLog(ctx, repositoryTypes.CreatePredictInferenceModelLog{
			TenantID: party.tenantID, TenantName: party.tenantID, UserID: marker(index, party.kind),
			Email: email, Name: name, StudyInstanceUID: "study-" + marker(index, party.kind),
		})
	case (&esentity.RetrievedStudy{}).GetModelName():
		_, err = repo.InsertRetrieveStudyLog(ctx, repositoryTypes.CreateRetrieveStudyLog{
			TenantID: party.tenantID, TenantName: party.tenantID, ModalityID: "modality",
			UserID: marker(index, party.kind), Email: email, Name: name,
			StudyInstanceUID: "study-" + marker(index, party.kind),
		})
	case (&esentity.SignedConsent{}).GetModelName():
		_, err = repo.InsertSignedConsentLog(ctx, repositoryTypes.CreateSignedConsentLog{
			TenantID: party.tenantID, TenantName: party.tenantID, UserID: marker(index, party.kind), Email: email,
		})
	case (&esentity.StoredCustomSeries{}).GetModelName():
		_, err = repo.InsertStoredCustomSeriesLog(ctx, repositoryTypes.CreateStoredCustomSeriesLog{
			TenantID: party.tenantID, TenantName: party.tenantID, UserID: marker(index, party.kind),
			Email: email, Name: name, PatientID: "patient-" + marker(index, party.kind),
			StudyInstanceUID: "study-" + marker(index, party.kind),
		})
	default:
		t.Fatalf("unknown audit index %s", index)
	}
	require.NoError(t, err)
}

func auditIndices() []string {
	return []string{
		(&esentity.AdminInvite{}).GetModelName(),
		(&esentity.AdminMember{}).GetModelName(),
		(&esentity.Login{}).GetModelName(),
		(&esentity.ModalityStudy{}).GetModelName(),
		(&esentity.PredictInferenceModel{}).GetModelName(),
		(&esentity.RetrievedStudy{}).GetModelName(),
		(&esentity.SignedConsent{}).GetModelName(),
		(&esentity.StoredCustomSeries{}).GetModelName(),
	}
}

func assertExactCallerTenant(t *testing.T, body, index string) {
	t.Helper()
	require.Contains(t, body, marker(index, "own"))
	require.Contains(t, body, callerTenant)
	assertNoOtherTenant(t, body, index)
}

func assertNoOtherTenant(t *testing.T, body, index string) {
	t.Helper()
	require.NotContains(t, body, marker(index, "victim"))
	require.NotContains(t, body, marker(index, "permuted"))
	require.NotContains(t, body, marker(index, "disjoint"))
	require.NotContains(t, body, victimTenant)
	require.NotContains(t, body, permutedTenant)
	require.NotContains(t, body, disjointTenant)
}

func getLogs(t *testing.T, handler http.Handler, index, day, query string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, logsRequest(index, day, query, false))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	return recorder.Body.String()
}

func getLogsExport(t *testing.T, handler http.Handler, index, day string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, logsRequest(index, day, "", true))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Header().Get("Content-Type"), "text/csv")
	return recorder.Body.String()
}

func logsRequest(index, day, query string, export bool) *http.Request {
	values := url.Values{}
	values.Set("index", index)
	values.Set("startDate", day)
	values.Set("endDate", day)
	if query != "" {
		values.Set("query", query)
	}
	if export {
		values.Set("export", "true")
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/ecs/logs?"+values.Encode(), nil)
	request.Header.Set("Authorization", "Bearer "+callerSession)
	return request
}

func logsRouter(controller *esrest.ElasticsearchQueryController, tenantID string, logsView bool) http.Handler {
	middleware := &iammiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &sessionCommand{},
		IAMQueryServiceInterface:   &sessionQuery{tenantID: tenantID},
		AccessResolver:             accessResolver{logsView: logsView},
	}
	router := chi.NewRouter()
	router.Route("/v1/ecs", func(r chi.Router) {
		r.Use(middleware.TokenSessionAuthGuard)
		r.Use(middleware.RequirePermission(accessEntity.PermLogsView))
		r.Get("/logs", controller.SearchDocumentLogs)
	})
	return router
}

type sessionQuery struct {
	iamapplication.IAMQueryServiceInterface
	tenantID string
}

func (query *sessionQuery) GetSessionToken(_ context.Context, key string) (iamentity.TokenSession, error) {
	if key != callerSession {
		return iamentity.TokenSession{}, errors.New(apiError.MissingRecord)
	}
	return iamentity.TokenSession{TenantID: query.tenantID, UserID: "user-caller", Role: iamentity.UserRole}, nil
}

func (query *sessionQuery) IsUserSuspended(context.Context, string, string) (bool, error) {
	return false, nil
}

type sessionCommand struct {
	iamapplication.IAMCommandServiceInterface
}

func (sessionCommand) SetTokenSession(context.Context, iamservice.SetTokenSession) error {
	return nil
}

type accessResolver struct {
	logsView bool
}

func (resolver accessResolver) Resolve(context.Context, string, string, string) (accessEntity.Access, error) {
	if !resolver.logsView {
		return accessEntity.ForRole(iamentity.UserRole, nil), nil
	}
	return accessEntity.ForRole(iamentity.UserRole, &accessEntity.CustomRole{
		ID:          "role-logs",
		Name:        "Logs",
		Permissions: []string{accessEntity.PermLogsView},
		AllModels:   true,
	}), nil
}

func requireElasticsearch871(t *testing.T, rawURL string) {
	t.Helper()
	response, err := http.Get(rawURL)
	require.NoError(t, err)
	defer response.Body.Close()
	var info struct {
		Version struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&info))
	require.Equal(t, "8.7.1", info.Version.Number)
}
