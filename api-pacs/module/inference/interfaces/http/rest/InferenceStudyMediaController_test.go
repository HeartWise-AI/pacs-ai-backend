package rest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	inferenceService "api-pacs/module/inference/infrastructure/service"
)

type fakeStudyMedia struct {
	tenant, study, kind, view, mediaID string
	listErr, openErr                   error
}

func (f *fakeStudyMedia) ListStudyMedia(_ context.Context, tenantID, studyInstanceUID, kind, view string) (inferenceService.StudyMediaList, error) {
	f.tenant, f.study, f.kind, f.view = tenantID, studyInstanceUID, kind, view
	if f.listErr != nil {
		return inferenceService.StudyMediaList{}, f.listErr
	}
	return inferenceService.StudyMediaList{StudyInstanceUID: studyInstanceUID, Items: []map[string]interface{}{{"mediaId": "m-1"}}}, nil
}

func (f *fakeStudyMedia) OpenStudyMedia(_ context.Context, tenantID, mediaID string) (inferenceService.StudyMediaFile, error) {
	f.tenant, f.mediaID = tenantID, mediaID
	if f.openErr != nil {
		return inferenceService.StudyMediaFile{}, f.openErr
	}
	return inferenceService.StudyMediaFile{Body: io.NopCloser(strings.NewReader("webp")), ContentType: "image/webp", ContentLength: 4, ETag: `"e"`}, nil
}

func mediaRouter(provider StudyMediaProvider) http.Handler {
	controller := &InferenceQueryController{StudyMedia: provider}
	router := chi.NewRouter()
	router.Get("/worklist/studies/{studyInstanceUID}/media", controller.GetStudyMedia)
	router.Get("/media/{mediaId}", controller.GetStudyMediaFile)
	return router
}

func mediaRequest(path, tenant string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if tenant != "" {
		request = request.WithContext(context.WithValue(request.Context(), iamTypes.TenantIDCtx, tenant))
	}
	return request
}

func TestGetStudyMediaUsesTheSessionTenant(t *testing.T) {
	provider := &fakeStudyMedia{}
	recorder := httptest.NewRecorder()

	mediaRouter(provider).ServeHTTP(recorder, mediaRequest("/worklist/studies/1.2.3/media?kind=loop&view=A4C", "tenant-a"))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, []string{"tenant-a", "1.2.3", "loop", "A4C"}, []string{provider.tenant, provider.study, provider.kind, provider.view})
	require.Contains(t, recorder.Body.String(), `"mediaId":"m-1"`)
}

func TestGetStudyMediaRejectsBadInputAndMissingTenant(t *testing.T) {
	for path, want := range map[string]int{
		"/worklist/studies/1.2.3/media?kind=gif":                         http.StatusBadRequest,
		"/worklist/studies/1.2.3/media?view=" + strings.Repeat("x", 129): http.StatusBadRequest,
	} {
		recorder := httptest.NewRecorder()
		mediaRouter(&fakeStudyMedia{}).ServeHTTP(recorder, mediaRequest(path, "tenant-a"))
		require.Equal(t, want, recorder.Code, path)
	}
	recorder := httptest.NewRecorder()
	mediaRouter(&fakeStudyMedia{}).ServeHTTP(recorder, mediaRequest("/worklist/studies/1.2.3/media", ""))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestGetStudyMediaFileStreamsWithNoStore(t *testing.T) {
	provider := &fakeStudyMedia{}
	recorder := httptest.NewRecorder()

	mediaRouter(provider).ServeHTTP(recorder, mediaRequest("/media/0b5c-77aa", "tenant-a"))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "webp", recorder.Body.String())
	require.Equal(t, "image/webp", recorder.Header().Get("Content-Type"))
	require.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	require.Equal(t, `"e"`, recorder.Header().Get("ETag"))
	require.Equal(t, "tenant-a", provider.tenant)
}

func TestGetStudyMediaFileMapsErrors(t *testing.T) {
	recorder := httptest.NewRecorder()
	mediaRouter(&fakeStudyMedia{}).ServeHTTP(recorder, mediaRequest("/media/bad..id", "tenant-a"))
	require.Equal(t, http.StatusNotFound, recorder.Code)

	recorder = httptest.NewRecorder()
	mediaRouter(&fakeStudyMedia{openErr: errors.New(apiError.MissingRecord)}).ServeHTTP(recorder, mediaRequest("/media/m-1", "tenant-a"))
	require.Equal(t, http.StatusNotFound, recorder.Code)

	recorder = httptest.NewRecorder()
	mediaRouter(&fakeStudyMedia{openErr: errors.New("boom")}).ServeHTTP(recorder, mediaRequest("/media/m-1", "tenant-a"))
	require.Equal(t, http.StatusInternalServerError, recorder.Code)

	recorder = httptest.NewRecorder()
	mediaRouter(nil).ServeHTTP(recorder, mediaRequest("/media/m-1", "tenant-a"))
	require.Equal(t, http.StatusNotFound, recorder.Code)
}
