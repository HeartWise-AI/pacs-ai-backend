package types

type CreateAdminMemberLog struct {
	TenantID   string
	TenantName string
	UserID     string
	Email      string
	Name       string
	Role       string
	LicenseNo  string
	Specialty  string
	Action     string
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
