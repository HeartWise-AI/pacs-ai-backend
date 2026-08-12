package types

type CreateAdminInviteLog struct {
	TenantID   string
	TenantName string
	Email      string
}

type CreateAdminMemberLog struct {
	TenantID      string
	TenantName    string
	UserID        string
	Email         string
	Name          string
	Role          string
	LicenseNo     string
	Specialty     string
	Action        string
	ActorUserID   string
	ActorRole     string
	PreviousState string
	NewState      string
	Reason        string
	Outcome       string
	FailureReason string
}

type CreateLoginLog struct {
	SessionID  string
	TenantID   string
	TenantName string
	UserID     string
	Email      string
	Name       string
	Role       string
	Specialty  string
}

type CreateGetModalityStudyLog struct {
	TenantID   string
	TenantName string
	ModalityID string
	UserID     string
	Email      string
	Name       string
	QueryID    string
}

type CreatePredictInferenceModelLog struct {
	TenantID           string
	TenantName         string
	UserID             string
	Email              string
	Name               string
	ContainerID        string
	ContainerName      string
	InferenceModelID   string
	InferenceModelName string
	DockerImage        string
	Model              string
	StudyInstanceUID   string
	SeriesInstanceUIDs []string
	AdditionalMetadata map[string]interface{}
}

type CreateRetrieveStudyLog struct {
	TenantID         string
	TenantName       string
	ModalityID       string
	UserID           string
	Email            string
	Name             string
	StudyInstanceUID string
}

type CreateSignedConsentLog struct {
	TenantID   string
	TenantName string
	UserID     string
	Email      string
}

type CreateStoredCustomSeriesLog struct {
	TenantID                string
	TenantName              string
	UserID                  string
	Email                   string
	Name                    string
	ModalityID              string
	StudyInstanceUID        string
	SeriesInstanceUIDs      []string
	PatientID               string
	ModelName               string
	ModelVersion            string
	CustomSeriesInstanceUID string
	CustomSOPInstanceUID    string
}

type SearchDocument struct {
	TenantID  string
	Query     string
	StartDate uint
	EndDate   uint
}

type CreateDataView struct {
	Title string
	Name  string
}
