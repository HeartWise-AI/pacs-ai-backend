package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/user/application"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
)

type tutorialResetCommandService struct {
	application.UserCommandServiceInterface
	context context.Context
	request serviceTypes.ResetTutorial
}

func (service *tutorialResetCommandService) ResetTutorial(ctx context.Context, data serviceTypes.ResetTutorial) error {
	service.context = ctx
	service.request = data
	return nil
}

func TestResetTutorialUsesAuthenticatedTenantAndUser(t *testing.T) {
	service := &tutorialResetCommandService{}
	controller := UserCommandController{UserCommandServiceInterface: service}
	request := httptest.NewRequest(http.MethodPost, "/v1/user/tutorial/reset", nil)
	request = request.WithContext(context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a"))
	request = request.WithContext(context.WithValue(request.Context(), iamTypes.UserIDCtx, "user-a"))
	recorder := httptest.NewRecorder()

	controller.ResetTutorial(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, serviceTypes.ResetTutorial{TenantID: "tenant-a", UserID: "user-a"}, service.request)
	require.Same(t, request.Context(), service.context)
}
