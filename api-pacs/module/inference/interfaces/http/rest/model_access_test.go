package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/accessctx"
	accessEntity "api-pacs/module/access/domain/entity"
	iamEntity "api-pacs/module/iam/domain/entity"
	"api-pacs/module/inference/infrastructure/service/types"
)

type stubArtifactResolver struct {
	ids map[string][]string
	err error
}

func (resolver *stubArtifactResolver) ResolveModelArtifactIDs(_ context.Context, _ string, ref string) ([]string, error) {
	if resolver.err != nil {
		return nil, resolver.err
	}
	ids, ok := resolver.ids[ref]
	if !ok {
		return nil, errors.New(apiError.MissingRecord)
	}
	return ids, nil
}

func restrictedAccess(modelIDs ...string) accessEntity.Access {
	return accessEntity.ForRole(iamEntity.UserRole, &accessEntity.CustomRole{ModelIDs: modelIDs})
}

func requestWith(access *accessEntity.Access) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	if access != nil {
		request = request.WithContext(accessctx.WithAccess(request.Context(), *access))
	}
	return request
}

func TestAuthorizeModelRef(t *testing.T) {
	resolver := &stubArtifactResolver{ids: map[string][]string{
		"reg-panecho": {"PanEcho"}, "container-rv": {"DeepRV_CLIP"}, "PanEcho": {"PanEcho"},
	}}
	restricted := restrictedAccess("PanEcho")
	everything := accessEntity.ForRole(iamEntity.UserRole, nil)

	require.Equal(t, "", authorizeModelRef(requestWith(&everything), nil, "t1", "anything"), "unrestricted users need no lookup")
	require.Equal(t, "", authorizeModelRef(requestWith(&restricted), resolver, "t1", "reg-panecho"))
	require.Equal(t, "", authorizeModelRef(requestWith(&restricted), resolver, "t1", "PanEcho"))
	require.Equal(t, apiError.ForbiddenAccess, authorizeModelRef(requestWith(&restricted), resolver, "t1", "container-rv"))
	require.Equal(t, apiError.ForbiddenAccess, authorizeModelRef(requestWith(&restricted), resolver, "t1", "unknown"))
	require.Equal(t, apiError.AccessUnavailable, authorizeModelRef(requestWith(&restricted), nil, "t1", "reg-panecho"))
	require.Equal(t, apiError.AccessUnavailable, authorizeModelRef(requestWith(&restricted), &stubArtifactResolver{err: errors.New(apiError.AccessUnavailable)}, "t1", "reg-panecho"))
	require.Equal(t, apiError.AccessUnavailable, authorizeModelRef(requestWith(nil), resolver, "t1", "reg-panecho"), "no session access")
}

func TestFilterAvailableModels(t *testing.T) {
	models := []types.GetInferenceAvailableModelResult{{ModelID: "PanEcho"}, {ModelID: "DeepRV_CLIP"}, {ModelID: "CathEF_CLIP"}}
	kept := filterAvailableModels(restrictedAccess("PanEcho", "CathEF_CLIP"), models)
	require.Equal(t, []types.GetInferenceAvailableModelResult{{ModelID: "PanEcho"}, {ModelID: "CathEF_CLIP"}}, kept)
	require.Len(t, filterAvailableModels(accessEntity.ForRole(iamEntity.AdminRole, nil), models), 3)
	require.Empty(t, filterAvailableModels(restrictedAccess(), models))
}

func TestFilterLatestResults(t *testing.T) {
	panEcho, rv := "PanEcho", "DeepRV_CLIP"
	results := types.StudyLatestModelResults{StudyInstanceUID: "1.2", Results: []types.StudyLatestModelResult{
		{ModelName: "PanEcho", ArtifactModelID: &panEcho},
		{ModelName: "DeepRV-CLIP", ArtifactModelID: &rv},
		{ModelName: "legacy"},
	}}
	kept := filterLatestResults(restrictedAccess("PanEcho"), results)
	require.Equal(t, "1.2", kept.StudyInstanceUID)
	require.Len(t, kept.Results, 1)
	require.Equal(t, "PanEcho", kept.Results[0].ModelName)
	require.Len(t, filterLatestResults(accessEntity.ForRole(iamEntity.UserRole, nil), results).Results, 3)
}

func TestFilterLatestResultsAlsoFiltersTheListedRuns(t *testing.T) {
	panEcho, rv := "PanEcho", "DeepRV_CLIP"
	results := types.StudyLatestModelResults{Results: []types.StudyLatestModelResult{{
		ModelName: "PanEcho", ArtifactModelID: &panEcho,
		Runs: []types.StudyLatestModelResult{
			{StudyServiceJobID: "job-new", ArtifactModelID: &panEcho},
			{StudyServiceJobID: "job-other-registration", ArtifactModelID: &rv},
			{StudyServiceJobID: "job-unresolved"},
			{StudyServiceJobID: "job-old", ArtifactModelID: &panEcho},
		},
	}}}

	kept := filterLatestResults(restrictedAccess("PanEcho"), results)

	require.Len(t, kept.Results, 1)
	require.Len(t, kept.Results[0].Runs, 2)
	require.Equal(t, "job-new", kept.Results[0].Runs[0].StudyServiceJobID)
	require.Equal(t, "job-old", kept.Results[0].Runs[1].StudyServiceJobID)
	require.Len(t, results.Results[0].Runs, 4, "the caller's results are not modified")
	require.Len(t, filterLatestResults(accessEntity.ForRole(iamEntity.AdminRole, nil), results).Results[0].Runs, 4)
}

func TestWriteModelAccessError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeModelAccessError(recorder, apiError.ForbiddenAccess)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	recorder = httptest.NewRecorder()
	writeModelAccessError(recorder, apiError.AccessUnavailable)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestResearchModelsAreHiddenFromEveryModelUsers(t *testing.T) {
	t.Setenv("PACS_RESEARCH_MODEL_IDS", "EchoAlign")
	resolver := &stubArtifactResolver{ids: map[string][]string{"reg-echoalign": {"EchoAlign"}, "reg-panecho": {"PanEcho"}}}
	base := accessEntity.ForRole(iamEntity.UserRole, nil)
	allModelsRole := accessEntity.ForRole(iamEntity.UserRole, &accessEntity.CustomRole{ID: "c", AllModels: true})
	research := restrictedAccess("EchoAlign")
	admin := accessEntity.ForRole(iamEntity.AdminRole, nil)

	for _, access := range []accessEntity.Access{base, allModelsRole} {
		require.Equal(t, apiError.ForbiddenAccess, authorizeModelRef(requestWith(&access), resolver, "t1", "reg-echoalign"))
		require.Equal(t, "", authorizeModelRef(requestWith(&access), resolver, "t1", "reg-panecho"))
		require.Equal(t, "", authorizeModelRef(requestWith(&access), resolver, "t1", "unregistered"), "unknown refs keep working")
		require.Equal(t, apiError.ForbiddenAccess, authorizeModelRef(requestWith(&access), resolver, "t1", "EchoAlign"), "the modelId itself is refused even when it does not resolve")
		require.Equal(t, apiError.ForbiddenAccess, authorizeModelRef(requestWith(&access), nil, "t1", " EchoAlign "))
		require.Equal(t, "", authorizeModelRef(requestWith(&access), &stubArtifactResolver{err: errors.New(apiError.AccessUnavailable)}, "t1", "reg-panecho"))
	}
	require.Equal(t, "", authorizeModelRef(requestWith(&research), resolver, "t1", "reg-echoalign"))
	require.Equal(t, "", authorizeModelRef(requestWith(&admin), nil, "t1", "reg-echoalign"))

	models := []types.GetInferenceAvailableModelResult{{ModelID: "PanEcho"}, {ModelID: "EchoAlign"}, {ModelID: ""}}
	require.Equal(t, []types.GetInferenceAvailableModelResult{{ModelID: "PanEcho"}, {ModelID: ""}}, filterAvailableModels(base, models))
	require.Equal(t, []types.GetInferenceAvailableModelResult{{ModelID: "PanEcho"}, {ModelID: ""}}, filterAvailableModels(allModelsRole, models))
	require.Equal(t, []types.GetInferenceAvailableModelResult{{ModelID: "EchoAlign"}}, filterAvailableModels(research, models))
	require.Len(t, filterAvailableModels(admin, models), 3)

	panEcho, echoAlign := "PanEcho", "EchoAlign"
	results := types.StudyLatestModelResults{Results: []types.StudyLatestModelResult{
		{ModelName: "PanEcho", ArtifactModelID: &panEcho},
		{ModelName: "EchoAlign", ArtifactModelID: &echoAlign},
		{ModelName: "legacy"},
		{ModelName: "PanEcho runs", ArtifactModelID: &panEcho, Runs: []types.StudyLatestModelResult{
			{StudyServiceJobID: "a", ArtifactModelID: &panEcho}, {StudyServiceJobID: "b", ArtifactModelID: &echoAlign}, {StudyServiceJobID: "c"},
		}},
	}}
	kept := filterLatestResults(base, results)
	require.Len(t, kept.Results, 3)
	require.Equal(t, "legacy", kept.Results[1].ModelName, "results of unknown models stay visible")
	require.Len(t, kept.Results[2].Runs, 2)
	require.Len(t, filterLatestResults(admin, results).Results, 4)

	allow, code := resultModelFilter(requestWith(&base))
	require.Equal(t, "", code)
	require.NotNil(t, allow)
	require.False(t, allow("EchoAlign"))
	require.True(t, allow(""))
	allow, _ = resultModelFilter(requestWith(&admin))
	require.Nil(t, allow)
}

func TestFilterRunExecutionsHidesResearchModelsFromUsers(t *testing.T) {
	t.Setenv("PACS_RESEARCH_MODEL_IDS", "EchoAlign")
	executions := []types.ProcessingRunExecutionSummary{
		{ExecutionID: "a", ModelName: "PanEcho"},
		{ExecutionID: "b", ModelName: "EchoAlign (research use only)"},
		{ExecutionID: "c", ModelName: "EchoAlign"},
	}
	base := accessEntity.ForRole(iamEntity.UserRole, nil)
	kept := filterRunExecutions(base, executions)
	require.Len(t, kept, 1)
	require.Equal(t, "a", kept[0].ExecutionID)
	require.Len(t, filterRunExecutions(accessEntity.ForRole(iamEntity.AdminRole, nil), executions), 3)
	require.Len(t, filterRunExecutions(restrictedAccess("EchoAlign", "PanEcho"), executions), 3)
}
