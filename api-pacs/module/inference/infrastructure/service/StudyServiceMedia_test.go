package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
)

func newMediaDispatcher(t *testing.T, handler http.HandlerFunc) *StudyServiceDispatcher {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &StudyServiceDispatcher{
		StudyServiceBaseURL:       server.URL,
		StudyServiceOperatorToken: "operator-token",
		StudyServiceClient:        server.Client(),
	}
}

func TestListStudyMediaSendsTenantAndTokenAndRewritesURLs(t *testing.T) {
	dispatcher := newMediaDispatcher(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/studies/1.2.3/media", r.URL.Path)
		require.Equal(t, "keyframe", r.URL.Query().Get("kind"))
		require.Equal(t, "Left Coronary", r.URL.Query().Get("view"))
		require.Empty(t, r.URL.Query().Get("include_phi_risk"))
		require.Equal(t, "tenant-a", r.Header.Get("X-Tenant-ID"))
		require.Equal(t, "Bearer operator-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"studyInstanceUID":"1.2.3","items":[
			{"mediaId":"m-1","url":"/media/m-1","kind":"keyframe","phiRisk":false},
			{"mediaId":"m-2","url":"/media/m-2","kind":"keyframe","phiRisk":true},
			{"url":"/media/none"}]}`)
	})

	list, err := dispatcher.ListStudyMedia(context.Background(), "tenant-a", "1.2.3", "keyframe", "Left Coronary")

	require.NoError(t, err)
	require.Equal(t, "1.2.3", list.StudyInstanceUID)
	require.Len(t, list.Items, 1)
	require.Equal(t, "/v1/inference/media/m-1", list.Items[0]["url"])
}

func TestListStudyMediaWithoutTenantIsRejectedAndMissingStudyIsEmpty(t *testing.T) {
	dispatcher := newMediaDispatcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := dispatcher.ListStudyMedia(context.Background(), " ", "1.2.3", "", "")
	require.EqualError(t, err, apiError.UnauthorizedAccess)

	list, err := dispatcher.ListStudyMedia(context.Background(), "tenant-a", "1.2.3", "", "")
	require.NoError(t, err)
	require.Empty(t, list.Items)
}

func TestOpenStudyMediaStreamsImagesOnly(t *testing.T) {
	contentType := "image/png"
	status := http.StatusOK
	dispatcher := newMediaDispatcher(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/media/m-1", r.URL.Path)
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "png-bytes")
	})

	file, err := dispatcher.OpenStudyMedia(context.Background(), "tenant-a", "m-1")
	require.NoError(t, err)
	body, _ := io.ReadAll(file.Body)
	file.Body.Close()
	require.Equal(t, "png-bytes", string(body))
	require.Equal(t, "image/png", file.ContentType)
	require.Equal(t, `"abc"`, file.ETag)

	contentType = "text/html"
	_, err = dispatcher.OpenStudyMedia(context.Background(), "tenant-a", "m-1")
	require.Error(t, err)

	contentType, status = "image/png", http.StatusGone
	_, err = dispatcher.OpenStudyMedia(context.Background(), "tenant-a", "m-1")
	require.EqualError(t, err, apiError.MissingRecord)
}
