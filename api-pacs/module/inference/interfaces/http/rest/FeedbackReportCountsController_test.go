package rest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

func TestListFeedbackReportCountsUsesTheSessionAndTheRange(t *testing.T) {
	from := time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 23, 5, 0, 0, 0, time.UTC)
	service := &predictionsServiceFake{counts: serviceTypes.FeedbackReportCounts{
		From: from, To: to,
		Models: []serviceTypes.FeedbackReportCountModel{{ModelName: "PanEcho", ModelVersion: "1.0.3", Modality: "echocardiogram", CompletedCount: 789}},
	}}
	controller := InferencePredictionsController{InferencePredictionsServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.ListFeedbackReportCounts(recorder, predictionsRequest(
		"/model/feedback/report-counts?tenantId=tenant-b&role=OWNER&from=2026-10-24T00:00:00-04:00&to=2026-11-23T05:00:00Z",
		predictionsIdentityFor("ADMIN"), nil,
	))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Len(t, service.countInputs, 1)
	input := service.countInputs[0]
	require.Equal(t, "tenant-a", input.TenantID)
	require.Equal(t, "ADMIN", input.Role)
	require.Equal(t, from, *input.From)
	require.Equal(t, to, *input.To)
	require.JSONEq(t, `{
		"from":"2026-10-24T04:00:00Z","to":"2026-11-23T05:00:00Z",
		"models":[{"modelName":"PanEcho","modelVersion":"1.0.3","modality":"echocardiogram","completedCount":789}]
	}`, string(mustDataJSON(t, recorder.Body.Bytes())))
}

func TestListFeedbackReportCountsRequiresBothBounds(t *testing.T) {
	for _, target := range []string{
		"/model/feedback/report-counts",
		"/model/feedback/report-counts?from=2026-10-24T00:00:00Z",
		"/model/feedback/report-counts?to=2026-10-24T00:00:00Z",
		"/model/feedback/report-counts?from=yesterday&to=2026-10-24T00:00:00Z",
		"/model/feedback/report-counts?from=2026-10-24T00:00:00Z&to=2026-10-25",
	} {
		service := &predictionsServiceFake{}
		recorder := httptest.NewRecorder()
		(&InferencePredictionsController{InferencePredictionsServiceInterface: service}).ListFeedbackReportCounts(
			recorder, predictionsRequest(target, predictionsIdentityFor("OWNER"), nil))
		require.Equal(t, http.StatusBadRequest, recorder.Code, target)
		require.Empty(t, service.countInputs, target)
	}
}

func TestListFeedbackReportCountsMapsErrors(t *testing.T) {
	for code, want := range map[string]int{
		apiError.InvalidPayload:  http.StatusBadRequest,
		apiError.ForbiddenAccess: http.StatusForbidden,
		apiError.DatabaseError:   http.StatusInternalServerError,
		apiError.HystrixTimeout:  http.StatusServiceUnavailable,
		"boom":                   http.StatusInternalServerError,
	} {
		service := &predictionsServiceFake{err: errors.New(code)}
		recorder := httptest.NewRecorder()
		(&InferencePredictionsController{InferencePredictionsServiceInterface: service}).ListFeedbackReportCounts(
			recorder, predictionsRequest("/model/feedback/report-counts?from=2026-10-24T00:00:00Z&to=2026-11-23T00:00:00Z", predictionsIdentityFor("USER"), nil))
		require.Equal(t, want, recorder.Code, code)
	}

	service := &predictionsServiceFake{}
	recorder := httptest.NewRecorder()
	(&InferencePredictionsController{InferencePredictionsServiceInterface: service}).ListFeedbackReportCounts(
		recorder, predictionsRequest("/model/feedback/report-counts?from=2026-10-24T00:00:00Z&to=2026-11-23T00:00:00Z", nil, nil))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Empty(t, service.countInputs)
}

func TestFeedbackReportCountsOpenAPIDocumentsTheRoute(t *testing.T) {
	paths := openAPIMap(t, loadWorklistOpenAPI(t)["paths"])
	operation := openAPIMap(t, openAPIMap(t, paths["/inference/model/feedback/report-counts"])["get"])
	responses := openAPIMap(t, operation["responses"])
	for _, code := range []string{"200", "400", "401", "403"} {
		require.Contains(t, responses, code)
	}
}
