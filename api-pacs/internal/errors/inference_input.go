package errors

// InferenceModelInputError carries the authoritative model series bounds to
// REST callers without exposing model payloads or DICOM identifiers.
type InferenceModelInputError struct {
	ErrorCode string
	Minimum   int
	Maximum   int
	Actual    int
}

func (err *InferenceModelInputError) Error() string {
	return err.ErrorCode
}
