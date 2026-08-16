package errors

const (
	// DatabaseError is the code for any database changes errors
	DatabaseError string = "DATABASE_ERROR"
	// DuplicateRecord is the code for duplicate records
	DuplicateRecord string = "DUPLICATE_RECORD"
	// ForbiddenAccess is the code for accessing forbidden resources
	ForbiddenAccess string = "FORBIDDEN_ACCESS"
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
	// AccountSuspended is returned when an account is blocked from creating or using sessions.
	AccountSuspended string = "ACCOUNT_SUSPENDED"
	// AccountAccessTransitionInProgress is returned when another account access change holds the per-user lock.
	AccountAccessTransitionInProgress string = "ACCOUNT_ACCESS_TRANSITION_IN_PROGRESS"
	// RegistrationRateLimited is returned when public registration exceeds an anti-abuse limit.
	RegistrationRateLimited string = "REGISTRATION_RATE_LIMITED"
	// LoginChallengeRequired tells the client to obtain a fresh login Turnstile token.
	LoginChallengeRequired string = "LOGIN_CHALLENGE_REQUIRED"
	// LoginRateLimited is returned when adaptive login protection reaches a hard limit.
	LoginRateLimited string = "LOGIN_RATE_LIMITED"
	// LoginProtectionUnavailable is returned when adaptive login protection cannot make a safe decision.
	LoginProtectionUnavailable string = "LOGIN_PROTECTION_UNAVAILABLE"
	// RequestBodyTooLarge is returned when an HTTP request exceeds its configured body limit.
	RequestBodyTooLarge string = "REQUEST_BODY_TOO_LARGE"
	// RequestInputLimitExceeded is returned when a decoded variable-length field exceeds a safe bound.
	RequestInputLimitExceeded string = "REQUEST_INPUT_LIMIT_EXCEEDED"
	// PolicyAcceptanceRequired is returned when one or more current required policies were omitted.
	PolicyAcceptanceRequired string = "POLICY_ACCEPTANCE_REQUIRED"
	// PolicyVersionStale is returned when a client submits a policy version that is no longer current.
	PolicyVersionStale string = "POLICY_VERSION_STALE"
	// PolicyConfigurationUnavailable fails closed when current policy metadata is invalid.
	PolicyConfigurationUnavailable string = "POLICY_CONFIGURATION_UNAVAILABLE"

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

	// Mailchimp-related errors
	MailchimpAPIError string = "MAILCHIMP_API_ERROR"

	// Mailgun-related errors
	MailgunError string = "MAILGUN_ERROR"

	// Inference-related errors
	DICOMParseError      string = "DICOM_PARSE_ERROR"
	DockerError          string = "DOCKER_ERROR"
	DockerInferenceError string = "DOCKER_INFERENCE_ERROR"
	InferenceError       string = "INFERENCE_ERROR"
	// InferenceQuotaExceeded is returned when the user's fixed-window allowance is exhausted.
	InferenceQuotaExceeded string = "INFERENCE_QUOTA_EXCEEDED"
	// InferenceConcurrencyExceeded is returned when the user's active reservation limit is reached.
	InferenceConcurrencyExceeded string = "INFERENCE_CONCURRENCY_EXCEEDED"
	// InferenceQuotaUnavailable is returned when quota enforcement cannot make a safe decision.
	InferenceQuotaUnavailable string = "INFERENCE_QUOTA_UNAVAILABLE"
	// InferenceModelInputOutOfRange is returned when the selected series count is outside the model contract.
	InferenceModelInputOutOfRange string = "INFERENCE_MODEL_INPUT_OUT_OF_RANGE"
	// InferenceInputInvalid is returned when inference UIDs or metadata violate bounded input rules.
	InferenceInputInvalid string = "INFERENCE_INPUT_INVALID"
	// InferenceModelConfigurationInvalid is returned when a model publishes unsafe upload bounds.
	InferenceModelConfigurationInvalid string = "INFERENCE_MODEL_CONFIGURATION_INVALID"
	TorchServeError                    string = "TORCHSERVE_ERROR"

	// Cloudflare-related errors
	CloudflareAPIError string = "CLOUDFLARE_API_ERROR"
	TurnstileInvalid   string = "TURNSTILE_INVALID"

	// Docusign-related errors
	DocusignError string = "DOCUSIGN_ERROR"
)
