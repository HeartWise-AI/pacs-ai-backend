package rest

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type forbiddenFeedbackCommandService struct {
	application.InferenceCommandServiceInterface
}

func (*forbiddenFeedbackCommandService) UpdateModelFeedback(context.Context, serviceTypes.UpdateModelFeedback) error {
	return errors.New(apiError.ForbiddenAccess)
}

func TestUpdateModelFeedbackReturnsForbiddenForUnownedFeedback(t *testing.T) {
	controller := &InferenceCommandController{
		InferenceCommandServiceInterface: &forbiddenFeedbackCommandService{},
	}
	body := bytes.NewBufferString(`{
		"id":"feedback-other-user",
		"inferenceModelId":"inference-model-a",
		"modelId":"model-a",
		"feedbackType":"APPROVE"
	}`)
	request := httptest.NewRequest(http.MethodPut, "/feedback/update", body)
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-a")
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()

	controller.UpdateModelFeedback(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
}
