package errors

type LoginError struct {
	Code              string
	ChallengeRequired bool
	RetryAfterSeconds int
}

func (err *LoginError) Error() string {
	return err.Code
}
