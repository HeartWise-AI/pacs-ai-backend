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

func TestWriteModelAccessError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeModelAccessError(recorder, apiError.ForbiddenAccess)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	recorder = httptest.NewRecorder()
	writeModelAccessError(recorder, apiError.AccessUnavailable)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
