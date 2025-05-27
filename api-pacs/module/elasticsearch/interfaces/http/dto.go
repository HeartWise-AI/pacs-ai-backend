package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"ForgotTenantUserPasswordRequest.TenantID": "Tenant ID is required.",
		"ForgotTenantUserPasswordRequest.Email":    "Email is required.",
		"LoginTenantUserRequest.TenantID":          "Tenant ID is required.",
		"LoginTenantUserRequest.IDToken":           "ID token is required.",
		"VerifyTenantUserEmailRequest.TenantID":    "Tenant ID is required.",
		"VerifyTenantUserEmailRequest.Email":       "Email is required.",
	}
)

type ForgotTenantUserPasswordRequest struct {
	TenantID string `json:"tenantId" validate:"required"`
	Email    string `json:"email" validate:"required"`
}

type LoginTenantUserRequest struct {
	TenantID string `json:"tenantId" validate:"required"`
	IDToken  string `json:"idToken" validate:"required"`
}

type VerifyTenantUserEmailRequest struct {
	TenantID string `json:"tenantId" validate:"required"`
	Email    string `json:"email" validate:"required"`
}

type AdminMemberLogResponse struct {
	TenantID   string `json:"sessionId" csv:"tenant_id"`
	TenantName string `json:"tenantName" csv:"tenant_name"`
	UserID     string `json:"userId" csv:"user_id"`
	Email      string `json:"email" csv:"email"`
	Name       string `json:"name" csv:"name"`
	Role       string `json:"role" csv:"role"`
	LicenseNo  string `json:"licenseNo" csv:"license_no"`
	Specialty  string `json:"specialty" csv:"specialty"`
	Action     string `json:"action" csv:"action"`
	Timestamp  uint   `json:"timestamp" csv:"timestamp"`
}

type LoginLogResponse struct {
	SessionID  string `json:"sessionId" csv:"session_id"`
	TenantID   string `json:"tenantId" csv:"tenant_id"`
	TenantName string `json:"tenantName" csv:"tenant_name"`
	UserID     string `json:"userId" csv:"user_id"`
	Email      string `json:"email" csv:"email"`
	Name       string `json:"name" csv:"name"`
	Role       string `json:"role" csv:"role"`
	Specialty  string `json:"specialty" csv:"specialty"`
	Timestamp  uint   `json:"timestamp" csv:"timestamp"`
}

type ModalityStudyLogResponse struct {
	TenantID   string `json:"tenantId" csv:"tenant_id"`
	TenantName string `json:"tenantName" csv:"tenant_name"`
	ModalityID string `json:"modalityId" csv:"modality_id"`
	UserID     string `json:"userId" csv:"user_id"`
	Email      string `json:"email" csv:"email"`
	Name       string `json:"name" csv:"name"`
	QueryID    string `json:"queryId" csv:"query_id"`
	Timestamp  uint   `json:"timestamp" csv:"timestamp"`
}

type PredictInferenceModelLogResponse struct {
	TenantID           string                 `json:"tenantId" csv:"tenant_id"`
	TenantName         string                 `json:"tenantName" csv:"tenant_name"`
	UserID             string                 `json:"userId" csv:"user_id"`
	Email              string                 `json:"email" csv:"email"`
	Name               string                 `json:"name" csv:"name"`
	ContainerID        string                 `json:"containerId" csv:"container_id"`
	ContainerName      string                 `json:"containerName" csv:"container_name"`
	InferenceModelID   string                 `json:"inferenceModelId" csv:"inference_model_id"`
	InferenceModelName string                 `json:"inferenceModelName" csv:"inference_model_name"`
	StudyInstanceUID   string                 `json:"studyInstanceUID" csv:"study_instance_uid"`
	SeriesInstanceUIDs []string               `json:"seriesInstanceUIDs" csv:"series_instance_uids"`
	AdditionalMetadata map[string]interface{} `json:"additionalMetadata" csv:"additional_metadata"`
	Timestamp          uint                   `json:"timestamp" csv:"timestamp"`
}

type RetrievedStudyLogResponse struct {
	TenantID         string `json:"tenantId" csv:"tenant_id"`
	TenantName       string `json:"tenantName"  csv:"tenant_name"`
	ModalityID       string `json:"modalityId" csv:"modality_id"`
	UserID           string `json:"userId" csv:"user_id"`
	Email            string `json:"email" csv:"email"`
	Name             string `json:"name" csv:"name"`
	StudyInstanceUID string `json:"studyInstanceUID" csv:"study_instance_uid"`
	QueryID          string `json:"queryId" csv:"query_id"`
	AnswerIndex      uint   `json:"answerIndex" csv:"answer_index"`
	Timestamp        uint   `json:"timestamp" csv:"timestamp"`
}

type StoredCustomSeriesLogResponse struct {
	TenantID                string   `json:"tenantId" csv:"tenant_id"`
	TenantName              string   `json:"tenantName" csv:"tenant_name"`
	UserID                  string   `json:"userId" csv:"user_id"`
	Email                   string   `json:"email" csv:"email"`
	Name                    string   `json:"name" csv:"name"`
	ModalityID              string   `json:"modalityId" csv:"modality_id"`
	StudyInstanceUID        string   `json:"studyInstanceUID" csv:"study_instance_uid"`
	SeriesInstanceUIDs      []string `json:"seriesInstanceUIDs" csv:"series_instance_uids"`
	PatientID               string   `json:"patientId" csv:"patient_id"`
	ModelName               string   `json:"modelName" csv:"model_name"`
	ModelVersion            string   `json:"modelVersion" csv:"model_version"`
	CustomSeriesInstanceUID string   `json:"customSeriesInstanceUID" csv:"custom_series_instance_uid"`
	CustomSOPInstanceUID    string   `json:"customSOPInstanceUID" csv:"custom_sop_instance_uid"`
	Timestamp               uint     `json:"timestamp" csv:"timestamp"`
}

type LoginTenantUserResponse struct {
	SessionToken string `json:"sessionToken"`
}
