package rest

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/application"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
)

type viewerTourQueryService struct {
	application.UserQueryServiceInterface
	userID string
	state  serviceTypes.UserViewerTourState
	err    error
}

func (service *viewerTourQueryService) GetUserViewerTourState(_ context.Context, userID string) (serviceTypes.UserViewerTourState, error) {
	service.userID = userID
	return service.state, service.err
}

type viewerTourCommandService struct {
	application.UserCommandServiceInterface
	completedFor string
	resetFor     string
	completedAt  int64
	err          error
}

func (service *viewerTourCommandService) CompleteUserViewerTour(_ context.Context, userID string) (serviceTypes.UserViewerTourState, error) {
	service.completedFor = userID
	return serviceTypes.UserViewerTourState{UserID: userID, ViewerTourCompletedAt: &service.completedAt}, service.err
}

func (service *viewerTourCommandService) ResetUserViewerTour(_ context.Context, userID string) (serviceTypes.UserViewerTourState, error) {
	service.resetFor = userID
	return serviceTypes.UserViewerTourState{UserID: userID}, service.err
}

type viewerTourBody struct {
	Success bool `json:"success"`
	Data    struct {
		UserID                string `json:"userId"`
		ViewerTourCompletedAt *int64 `json:"viewerTourCompletedAt"`
	} `json:"data"`
	ErrorCode string `json:"errorCode"`
}

func authenticatedViewerTourRequest(method, target, userID string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, userID)
	return request.WithContext(ctx)
}

func decodeViewerTourBody(t *testing.T, recorder *httptest.ResponseRecorder) viewerTourBody {
	t.Helper()
	var body viewerTourBody
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func TestGetUserViewerTourReturnsNullForAuthenticatedUserWhoHasNotCompletedIt(t *testing.T) {
	service := &viewerTourQueryService{state: serviceTypes.UserViewerTourState{UserID: "user-a"}}
	controller := UserQueryController{UserQueryServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.GetUserViewerTour(recorder, authenticatedViewerTourRequest(http.MethodGet, "/v1/user/viewer-tour", "user-a"))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "user-a", service.userID)
	require.Contains(t, recorder.Body.String(), `"viewerTourCompletedAt":null`)
	body := decodeViewerTourBody(t, recorder)
	require.True(t, body.Success)
	require.Equal(t, "user-a", body.Data.UserID)
	require.Nil(t, body.Data.ViewerTourCompletedAt)
}

func TestGetUserViewerTourReturnsCompletionTime(t *testing.T) {
	completedAt := int64(1763100000)
	service := &viewerTourQueryService{state: serviceTypes.UserViewerTourState{UserID: "user-a", ViewerTourCompletedAt: &completedAt}}
	controller := UserQueryController{UserQueryServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.GetUserViewerTour(recorder, authenticatedViewerTourRequest(http.MethodGet, "/v1/user/viewer-tour", "user-a"))

	require.Equal(t, http.StatusOK, recorder.Code)
	body := decodeViewerTourBody(t, recorder)
	require.NotNil(t, body.Data.ViewerTourCompletedAt)
	require.Equal(t, completedAt, *body.Data.ViewerTourCompletedAt)
}

func TestGetUserViewerTourReportsStoreFailure(t *testing.T) {
	service := &viewerTourQueryService{err: stderrors.New(apiError.FirestoreError)}
	controller := UserQueryController{UserQueryServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.GetUserViewerTour(recorder, authenticatedViewerTourRequest(http.MethodGet, "/v1/user/viewer-tour", "user-a"))

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	body := decodeViewerTourBody(t, recorder)
	require.False(t, body.Success)
	require.Equal(t, apiError.FirestoreError, body.ErrorCode)
}

func TestCompleteUserViewerTourUsesAuthenticatedUserOnly(t *testing.T) {
	service := &viewerTourCommandService{completedAt: 1763100000}
	controller := UserCommandController{UserCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	// any user id smuggled in the query string must be ignored
	controller.CompleteUserViewerTour(recorder, authenticatedViewerTourRequest(http.MethodPut, "/v1/user/viewer-tour/complete?userId=user-b", "user-a"))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "user-a", service.completedFor)
	body := decodeViewerTourBody(t, recorder)
	require.Equal(t, "user-a", body.Data.UserID)
	require.Equal(t, int64(1763100000), *body.Data.ViewerTourCompletedAt)
}

func TestResetUserViewerTourClearsCompletionForAuthenticatedUser(t *testing.T) {
	service := &viewerTourCommandService{}
	controller := UserCommandController{UserCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.ResetUserViewerTour(recorder, authenticatedViewerTourRequest(http.MethodPost, "/v1/user/viewer-tour/reset", "user-a"))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "user-a", service.resetFor)
	body := decodeViewerTourBody(t, recorder)
	require.Nil(t, body.Data.ViewerTourCompletedAt)
}

func TestResetUserViewerTourReportsStoreFailure(t *testing.T) {
	service := &viewerTourCommandService{err: stderrors.New(apiError.FirestoreError)}
	controller := UserCommandController{UserCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.ResetUserViewerTour(recorder, authenticatedViewerTourRequest(http.MethodPost, "/v1/user/viewer-tour/reset", "user-a"))

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.False(t, decodeViewerTourBody(t, recorder).Success)
}
