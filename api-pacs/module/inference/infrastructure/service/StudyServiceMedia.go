package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	apiError "api-pacs/internal/errors"
)

// Study media (CA-267 M4): study-service saves a keyframe, a thumbnail and a
// short loop per clip. api-pacs lists and serves them to signed-in users of the
// tenant. Images study-service flags as possibly carrying burned-in patient
// text are never requested here.

const (
	studyMediaListMaxBytes = 4 << 20
	// StudyMediaFileMaxBytes bounds one served file (loops are well below 1 MB).
	StudyMediaFileMaxBytes = 16 << 20
)

// StudyMediaFile is one media file read from study-service. The caller closes Body.
type StudyMediaFile struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	ETag          string
}

// StudyMediaList is the study-service list with each item's url pointing at api-pacs.
type StudyMediaList struct {
	StudyInstanceUID string                   `json:"studyInstanceUID"`
	Items            []map[string]interface{} `json:"items"`
}

// StudyMediaPath is the api-pacs path that serves one media file.
func StudyMediaPath(mediaID string) string {
	return "/v1/inference/media/" + url.PathEscape(mediaID)
}

func (service *StudyServiceDispatcher) studyMediaRequest(ctx context.Context, tenantID, path string, query url.Values) (*http.Response, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(service.StudyServiceBaseURL), "/")
	if baseURL == "" {
		return nil, errStudyServiceBaseURLMissing
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, errors.New(apiError.UnauthorizedAccess)
	}
	endpoint := baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Request-ID", generateID())
	req.Header.Set("X-Tenant-ID", tenantID)
	service.applyBearerToken(req, service.StudyServiceOperatorToken)
	resp, err := service.StudyServiceClient.Do(req)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		resp.Body.Close()
		return nil, errors.New(apiError.MissingRecord)
	case resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices:
		resp.Body.Close()
		return nil, &StudyServiceLookupHTTPError{StatusCode: resp.StatusCode}
	}
	return resp, nil
}

// ListStudyMedia lists the saved media of one study for the tenant.
func (service *StudyServiceDispatcher) ListStudyMedia(ctx context.Context, tenantID, studyInstanceUID, kind, view string) (StudyMediaList, error) {
	query := url.Values{}
	if kind = strings.TrimSpace(kind); kind != "" {
		query.Set("kind", kind)
	}
	if view = strings.TrimSpace(view); view != "" {
		query.Set("view", view)
	}
	resp, err := service.studyMediaRequest(ctx, tenantID,
		fmt.Sprintf("/studies/%s/media", url.PathEscape(strings.TrimSpace(studyInstanceUID))), query)
	if err != nil {
		if err.Error() == apiError.MissingRecord {
			return StudyMediaList{StudyInstanceUID: studyInstanceUID, Items: []map[string]interface{}{}}, nil
		}
		return StudyMediaList{}, err
	}
	defer resp.Body.Close()
	var list StudyMediaList
	if err := json.NewDecoder(io.LimitReader(resp.Body, studyMediaListMaxBytes)).Decode(&list); err != nil {
		return StudyMediaList{}, err
	}
	items := make([]map[string]interface{}, 0, len(list.Items))
	for _, item := range list.Items {
		mediaID, _ := item["mediaId"].(string)
		if mediaID == "" {
			continue
		}
		if phiRisk, _ := item["phiRisk"].(bool); phiRisk {
			continue // never requested, never passed on
		}
		item["url"] = StudyMediaPath(mediaID)
		items = append(items, item)
	}
	list.Items = items
	if list.StudyInstanceUID == "" {
		list.StudyInstanceUID = studyInstanceUID
	}
	return list, nil
}

// OpenStudyMedia streams one media file of the tenant.
func (service *StudyServiceDispatcher) OpenStudyMedia(ctx context.Context, tenantID, mediaID string) (StudyMediaFile, error) {
	resp, err := service.studyMediaRequest(ctx, tenantID, "/media/"+url.PathEscape(strings.TrimSpace(mediaID)), nil)
	if err != nil {
		return StudyMediaFile{}, err
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if contentType != "image/png" && contentType != "image/webp" {
		resp.Body.Close()
		return StudyMediaFile{}, fmt.Errorf("study-service media has unexpected content type %q", contentType)
	}
	return StudyMediaFile{
		Body:          resp.Body,
		ContentType:   contentType,
		ContentLength: resp.ContentLength,
		ETag:          resp.Header.Get("ETag"),
	}, nil
}
