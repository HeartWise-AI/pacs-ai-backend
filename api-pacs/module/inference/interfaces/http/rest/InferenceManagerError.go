package rest

import (
	stderrors "errors"
	"net/http"
	"strconv"
	"time"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
)

type retryableInferenceError interface {
	error
	HTTPStatus() int
	RetryAfter() time.Duration
}

func writeInferenceManagerError(w http.ResponseWriter, err error) bool {
	var retryable retryableInferenceError
	if !stderrors.As(err, &retryable) {
		return false
	}
	retryAfterSeconds := int64((retryable.RetryAfter() + time.Second - 1) / time.Second)
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
	response := viewmodels.HTTPResponseVM{
		Status:    retryable.HTTPStatus(),
		Success:   false,
		Message:   "Inference capacity is temporarily unavailable.",
		ErrorCode: apiError.InferenceAdmissionTimeout,
	}
	response.JSON(w)
	return true
}
