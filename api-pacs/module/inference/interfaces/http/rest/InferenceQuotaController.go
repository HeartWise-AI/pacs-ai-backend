package rest

import (
	stderrors "errors"
	"net/http"
	"strconv"
	"time"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	types "api-pacs/module/inference/interfaces/http"
)

// GetInferenceQuota returns the authenticated user's current allowance and
// active reservation state without exposing tenant or user identifiers.
func (controller *InferenceQueryController) GetInferenceQuota(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)
	status, err := controller.InferenceQueryServiceInterface.GetInferenceQuota(r.Context(), tenantID, userID)
	if err != nil {
		response := viewmodels.HTTPResponseVM{
			Status: http.StatusServiceUnavailable, Success: false,
			Message:   "Inference quota information is temporarily unavailable.",
			ErrorCode: apiError.InferenceQuotaUnavailable,
		}
		w.Header().Set("Cache-Control", "no-store")
		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status: http.StatusOK, Success: true,
		Message: "Successfully retrieved inference quota.",
		Data:    inferenceQuotaResponse(status),
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w)
}

func inferenceQuotaResponse(status serviceTypes.InferenceQuotaStatus) *types.InferenceQuotaResponse {
	var resetAt *uint64
	if status.ResetAfter > 0 {
		value := uint64(time.Now().Add(status.ResetAfter).Unix())
		resetAt = &value
	}
	return &types.InferenceQuotaResponse{
		Allowance:                    status.Allowance,
		Used:                         status.Used,
		Remaining:                    status.Remaining,
		WindowSeconds:                int64(status.Window / time.Second),
		ResetAfterSeconds:            durationSecondsCeilingForHTTP(status.ResetAfter),
		ResetAt:                      resetAt,
		MaxConcurrentExecutions:      status.MaxConcurrentExecutions,
		ActiveExecutions:             status.ActiveExecutions,
		ConcurrencyRetryAfterSeconds: durationSecondsCeilingForHTTP(status.ConcurrentRetryAfter),
	}
}

func writeInferenceQuotaError(w http.ResponseWriter, err error) bool {
	var limitError *apiError.InferenceQuotaLimitError
	if !stderrors.As(err, &limitError) {
		return false
	}
	retryAfter := limitError.RetryAfterSeconds
	if retryAfter <= 0 {
		retryAfter = limitError.ResetAfterSeconds
	}
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
	}
	w.Header().Set("Cache-Control", "no-store")
	message := "Inference allowance exhausted. Please try again after the quota resets."
	if limitError.ErrorCode == apiError.InferenceConcurrencyExceeded {
		message = "Too many inference executions are already active. Please try again later."
	}
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusTooManyRequests, Success: false, Message: message,
		ErrorCode: limitError.ErrorCode,
		Data: map[string]interface{}{
			"allowance":               limitError.Allowance,
			"used":                    limitError.Used,
			"remaining":               limitError.Remaining,
			"resetAfterSeconds":       limitError.ResetAfterSeconds,
			"maxConcurrentExecutions": limitError.MaxConcurrentExecutions,
			"activeExecutions":        limitError.ActiveExecutions,
			"retryAfterSeconds":       retryAfter,
		},
	}
	response.JSON(w)
	return true
}

func durationSecondsCeilingForHTTP(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64((duration + time.Second - 1) / time.Second)
}
