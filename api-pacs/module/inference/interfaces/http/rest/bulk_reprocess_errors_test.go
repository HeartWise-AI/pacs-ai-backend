package rest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
)

func TestBulkReprocessErrorPresentationMatchesWrappedErrors(t *testing.T) {
	for _, code := range []string{apiError.InvalidPayload, apiError.MissingRecord, apiError.DuplicateRecord, apiError.InferenceQuotaUnavailable} {
		plain, _, _ := bulkReprocessErrorPresentation(errors.New(code))
		require.Equal(t, code, plain)

		wrapped, _, _ := bulkReprocessErrorPresentation(fmt.Errorf("create run: %w", errors.New(code)))
		require.Equal(t, code, wrapped, "wrapped %s", code)

		twice, _, _ := bulkReprocessErrorPresentation(fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", errors.New(code))))
		require.Equal(t, code, twice, "wrapped twice %s", code)
	}
}

func TestBulkReprocessErrorPresentationDuplicateMessage(t *testing.T) {
	code, message, retry := bulkReprocessErrorPresentation(fmt.Errorf("plan: %w", errors.New(apiError.DuplicateRecord)))
	require.Equal(t, apiError.DuplicateRecord, code)
	require.Equal(t, "This study already has an active processing run.", message)
	require.Zero(t, retry)
}

func TestBulkReprocessErrorPresentationCancelledRequest(t *testing.T) {
	code, message, _ := bulkReprocessErrorPresentation(fmt.Errorf("dispatch: %w", context.Canceled))
	require.Equal(t, apiError.ServerError, code)
	require.Equal(t, "The request ended before this study was submitted.", message)
}

func TestBulkReprocessErrorPresentationUnknownError(t *testing.T) {
	code, message, _ := bulkReprocessErrorPresentation(errors.New("boom"))
	require.Equal(t, apiError.ServerError, code)
	require.Equal(t, "The processing service could not create a new run.", message)
}
