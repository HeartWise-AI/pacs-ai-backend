package errors

const (
	// DatabaseError is the code for any database changes errors
	DatabaseError string = "DATABASE_ERROR"
	// DuplicateRecord is the code for duplicate records
	DuplicateRecord string = "DUPLICATE_RECORD"
	// HystrixTimeout is the code for hystrix timeouts
	HystrixTimeout string = "HYSTRIX_TIMEOUT"
	// InvalidRequestPayload is the code for binding errors
	InvalidRequestPayload string = "INVALID_REQUEST_PAYLOAD"
	// InvalidPayload is the code for payload not satisfying requirements
	InvalidPayload string = "INVALID_PAYLOAD"
	// MaximumLimitReached is the code when the max limit is reached
	MaximumLimitReached string = "MAX_LIMIT_REACHED"
	// MissingAPIEndpoint is the code for 404 API endpoints
	MissingAPIEndpoint string = "MISSING_API_ENDPOINT"
	// MissingConfiguration is the code for configurations not found error
	MissingConfiguration string = "MISSING_CONFIGURATION"
	// MissingRecord is the code for no record found
	MissingRecord string = "MISSING_RECORD"
	// ServerError is the code for server error
	ServerError string = "SERVER_ERROR"
	// ServerMaintenance is the code for server maintenance
	ServerMaintenance string = "SERVER_MAINTENANCE"
	// StorageUploadFailed is the code when storage upload (like to s3) failed
	StorageUploadFailed string = "STORAGE_UPLOAD_FAILED"
	// SystemScriptFailed is the code when scripts failed
	SystemScriptFailed string = "SYSTEM_SCRIPT_FAILED"
	// UnauthorizedAccess is the code for accessing restricted routes
	UnauthorizedAccess string = "UNAUTHORIZED_ACCESS"

	// Firebase-related errors
	FirebaseAuthError            string = "FIREBASE_AUTH_ERROR"
	FirebaseAuthEmailNotVerified string = "FIREBASE_AUTH_EMAIL_NOT_VERIFIED"
	FirestoreError               string = "FIRESTORE_ERROR"

	// AWS-related errors
	SESError string = "SES_ERROR"

	// Orthanc-related errors
	OrthancError string = "ORTHANC_ERROR"

	// Kibana-related errors
	KibanaError           string = "KIBANA_ERROR"
	KibanaDuplicateRecord string = "KIBANA_DUPLICATE_RECORD"

	// Mailgun-related errors
	MailgunError string = "MAILGUN_ERROR"

	// Inference-related errors
	DICOMParseError      string = "DICOM_PARSE_ERROR"
	DockerError          string = "DOCKER_ERROR"
	DockerInferenceError string = "DOCKER_INFERENCE_ERROR"
	InferenceError       string = "INFERENCE_ERROR"
	TorchServeError      string = "TORCHSERVE_ERROR"
)
