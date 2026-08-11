package errors

// RegistrationRateLimitError carries retry metadata while keeping a stable,
// non-sensitive public error code.
type RegistrationRateLimitError struct {
	RetryAfterSeconds int
}

func (err *RegistrationRateLimitError) Error() string {
	return RegistrationRateLimited
}
