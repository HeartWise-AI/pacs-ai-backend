package modelupgrade

import (
	"errors"
	"net/http"

	apiError "api-pacs/internal/errors"
)

var (
	ErrInvalid  = errors.New("invalid model upgrade request")
	ErrConflict = errors.New("a model upgrade is already active")
	ErrNotFound = errors.New("model upgrade was not found")
)

type HTTPError struct {
	Status  int
	Code    string
	Message string
	Cause   error
}

func (err *HTTPError) Error() string     { return err.Message }
func (err *HTTPError) Unwrap() error     { return err.Cause }
func (err *HTTPError) HTTPStatus() int   { return err.Status }
func (err *HTTPError) ErrorCode() string { return err.Code }

func invalid(message string) error {
	return &HTTPError{Status: http.StatusBadRequest, Code: apiError.InferenceUpgradeInvalid, Message: message, Cause: ErrInvalid}
}

func conflict() error {
	return &HTTPError{Status: http.StatusConflict, Code: apiError.InferenceUpgradeConflict, Message: "A model upgrade is already active.", Cause: ErrConflict}
}

func sharedContainerConflict() error {
	return &HTTPError{
		Status:  http.StatusConflict,
		Code:    apiError.InferenceUpgradeConflict,
		Message: "The model shares its container with another registration and cannot be upgraded independently.",
		Cause:   ErrConflict,
	}
}

func cancellationConflict() error {
	return &HTTPError{Status: http.StatusConflict, Code: apiError.InferenceUpgradeConflict, Message: "This model upgrade can no longer be cancelled.", Cause: ErrConflict}
}

func notFound() error {
	return &HTTPError{Status: http.StatusNotFound, Code: apiError.InferenceUpgradeNotFound, Message: "Model upgrade not found.", Cause: ErrNotFound}
}
