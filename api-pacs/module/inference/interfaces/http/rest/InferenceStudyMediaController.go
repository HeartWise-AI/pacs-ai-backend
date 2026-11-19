package rest

import (
	"context"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	apiError "api-pacs/internal/errors"
	inferenceService "api-pacs/module/inference/infrastructure/service"
)

// StudyMediaProvider lists and opens the media study-service saved per clip
// (CA-267 M4). StudyServiceDispatcher implements it.
type StudyMediaProvider interface {
	ListStudyMedia(ctx context.Context, tenantID, studyInstanceUID, kind, view string) (inferenceService.StudyMediaList, error)
	OpenStudyMedia(ctx context.Context, tenantID, mediaID string) (inferenceService.StudyMediaFile, error)
}

var (
	studyMediaIDPattern = regexp.MustCompile(`^[0-9A-Za-z-]{1,64}$`)
	studyMediaKinds     = map[string]bool{"": true, "keyframe": true, "thumbnail": true, "loop": true}
)

const studyMediaViewMaxLength = 128

// GetStudyMedia lists the keyframes, thumbnails and loops of one study.
func (controller *InferenceQueryController) GetStudyMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeViewerResultsError(w, apiError.UnauthorizedAccess)
		return
	}
	if controller.StudyMedia == nil {
		writeViewerResultsError(w, apiError.MissingRecord)
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	view := strings.TrimSpace(r.URL.Query().Get("view"))
	if !studyMediaKinds[kind] || len(view) > studyMediaViewMaxLength {
		writeViewerResultsError(w, apiError.InvalidPayload)
		return
	}
	list, err := controller.StudyMedia.ListStudyMedia(r.Context(), tenantID, chi.URLParam(r, "studyInstanceUID"), kind, view)
	if err != nil {
		log.Printf("[study media] list failed tenant_id=%s err=%v", tenantID, err)
		writeViewerResultsError(w, err.Error())
		return
	}
	writeWorklistQuerySuccess(w, "Successfully retrieved study media.", list)
}

// GetStudyMediaFile serves one media file (PNG or WebP).
func (controller *InferenceQueryController) GetStudyMediaFile(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeViewerResultsError(w, apiError.UnauthorizedAccess)
		return
	}
	mediaID := chi.URLParam(r, "mediaId")
	if controller.StudyMedia == nil || !studyMediaIDPattern.MatchString(mediaID) {
		writeViewerResultsError(w, apiError.MissingRecord)
		return
	}
	file, err := controller.StudyMedia.OpenStudyMedia(r.Context(), tenantID, mediaID)
	if err != nil {
		if err.Error() != apiError.MissingRecord {
			log.Printf("[study media] open failed tenant_id=%s media_id=%s err=%v", tenantID, mediaID, err)
		}
		writeViewerResultsError(w, err.Error())
		return
	}
	defer file.Body.Close()
	header := w.Header()
	header.Set("Content-Type", file.ContentType)
	header.Set("Cache-Control", "private, no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Disposition", "inline")
	if file.ETag != "" {
		header.Set("ETag", file.ETag)
	}
	if file.ContentLength > 0 && file.ContentLength <= inferenceService.StudyMediaFileMaxBytes {
		header.Set("Content-Length", strconv.FormatInt(file.ContentLength, 10))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, io.LimitReader(file.Body, inferenceService.StudyMediaFileMaxBytes)); err != nil {
		log.Printf("[study media] copy failed media_id=%s err=%v", mediaID, err)
	}
}
