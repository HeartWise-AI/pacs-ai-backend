package errors

// InferenceQuotaLimitError carries stable quota metadata through the service
// boundary so REST callers can render an actionable HTTP 429 response.
type InferenceQuotaLimitError struct {
	ErrorCode               string
	Allowance               int64
	Used                    int64
	Remaining               int64
	ResetAfterSeconds       int64
	MaxConcurrentExecutions int64
	ActiveExecutions        int64
	RetryAfterSeconds       int64
}

func (err *InferenceQuotaLimitError) Error() string {
	return err.ErrorCode
}
