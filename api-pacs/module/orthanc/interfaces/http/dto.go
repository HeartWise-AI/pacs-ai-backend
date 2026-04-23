package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"FindModalityStudiesRequest.ModalityID":         "Modality ID is required",
		"RetrieveModalityStudyRequest.ModalityID":       "Modality ID is required",
		"RetrieveModalityStudyRequest.StudyInstanceUID": "Study instance uid is required.",
		"UpdateDICOMModalityRequest.AET":                "AET is required.",
		"UpdateDICOMModalityRequest.Host":               "Host is required.",
		"UpdateDICOMModalityRequest.Port":               "Port is required.",
	}
)

type FindModalityStudiesRequest struct {
	ModalityID                 string `json:"modalityId" validate:"required"`
	AccessionNumber            string `json:"accessionNumber"`
	InstitutionName            string `json:"institutionName"`
	ModalitiesInStudy          string `json:"modalitiesInStudy"`
	NumberOfStudyRelatedSeries string `json:"numberOfStudyRelatedSeries"`
	PatientBirthDate           string `json:"patientBirthDate"`
	PatientID                  string `json:"patientId"`
	PatientName                string `json:"patientName"`
	PatientSex                 string `json:"patientSex"`
	ReferringPhysicianName     string `json:"referringPhysicianName"`
	RequestingPhysician        string `json:"requestingPhysician"`
	StudyDate                  string `json:"studyDate"`
	StudyDescription           string `json:"studyDescription"`
	StudyID                    string `json:"studyId"`
	StudyInstanceUID           string `json:"studyInstanceUID"`
	StudyTime                  string `json:"studyTime"`
}

type RetrieveModalityStudyRequest struct {
	ModalityID       string `json:"modalityId" validate:"required"`
	StudyInstanceUID string `json:"studyInstanceUID" validate:"required"`
	ModalityType     string `json:"modalityType,omitempty"`
}

type UpdateDICOMModalityRequest struct {
	AET           string `json:"aet" validate:"required"`
	Host          string `json:"host" validate:"required"`
	Port          uint   `json:"port" validate:"required"`
	UseDicomTLS   bool   `json:"useDicomTLS"`
	CFindEnabled  bool   `json:"cFindEnabled"`
	CMoveEnabled  bool   `json:"cMoveEnabled"`
	CStoreEnabled bool   `json:"cStoreEnabled"`
}

type FindLocalSOPInstanceResponse struct {
	QueryIDs []string `json:"queryIds"`
}

type FindModalityStudiesResponse struct {
	QueryID string  `json:"queryId"`
	Studies []Study `json:"studies"`
}

type GetJobInfoResponse struct {
	CompletionTime   string                 `json:"completionTime"`
	Content          map[string]interface{} `json:"content"`
	CreationTime     string                 `json:"creationTime"`
	EffectiveRuntime float64                `json:"effectiveRuntime"`
	ErrorCode        int                    `json:"errorCode"`
	ErrorDescription string                 `json:"errorDescription"`
	ErrorDetails     interface{}            `json:"errorDetails"`
	ID               string                 `json:"id"`
	Priority         uint                   `json:"priority"`
	Progress         uint                   `json:"progress"`
	State            string                 `json:"state"`
	Timestamp        string                 `json:"timestamp"`
	Type             string                 `json:"type"`
}

type GetDICOMModalityResponse struct {
	ID            string `json:"id"`
	TenantID      string `json:"tenantId"`
	ModalityID    string `json:"modalityId"`
	AET           string `json:"aet"`
	HostHash      string `json:"hostHash"`
	CFindEnabled  bool   `json:"cFindEnabled"`
	CMoveEnabled  bool   `json:"cMoveEnabled"`
	CStoreEnabled bool   `json:"cStoreEnabled"`
	CreatedAt     int    `json:"createdAt"`
	UpdatedAt     int    `json:"updatedAt"`
}

type ListDICOMModalitiesResponse struct {
	Modalities map[string]ListDICOMModality `json:"modalities"`
}

type RetrieveQueryModalityAnswerResponse struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type ListDICOMModality struct {
	TenantID            string `json:"tenantId"`
	ModalityID          string `json:"modalityId"`
	AET                 string `json:"aet"`
	AllowEcho           bool   `json:"allowEcho"`
	AllowFind           bool   `json:"allowFind"`
	AllowFindWorklist   bool   `json:"allowFindWorklist"`
	AllowGet            bool   `json:"allowGet"`
	AllowMove           bool   `json:"allowMove"`
	AllowStore          bool   `json:"allowStore"`
	AllowTranscoding    bool   `json:"allowTranscoding"`
	Host                string `json:"host"`
	Port                uint   `json:"port"`
	Timeout             uint   `json:"timeout"`
	UseDicomTLS         bool   `json:"useDicomTLS"`
	TargetCFindEnabled  bool   `json:"targetCFindEnabled"`
	TargetCMoveEnabled  bool   `json:"targetCMoveEnabled"`
	TargetCStoreEnabled bool   `json:"targetCStoreEnabled"`
}

type Study struct {
	AccessionNumber            string `json:"accessionNumber"`
	ModalitiesInStudy          string `json:"modalitiesInStudy"`
	NumberOfStudyRelatedSeries string `json:"numberOfStudyRelatedSeries"`
	PatientBirthDate           string `json:"patientBirthDate"`
	PatientID                  string `json:"patientId"`
	PatientName                string `json:"patientName"`
	PatientSex                 string `json:"patientSex"`
	QueryRetrieveLevel         string `json:"queryRetrieveLevel"`
	ReferringPhysicianName     string `json:"referringPhysicianName"`
	RetrieveAETitle            string `json:"retrieveAETitle"`
	SpecificCharacterSet       string `json:"specificCharacterSet"`
	StudyDate                  string `json:"studyDate"`
	StudyDescription           string `json:"studyDescription"`
	StudyID                    string `json:"studyId"`
	StudyInstanceUID           string `json:"studyInstanceUID"`
	StudyTime                  string `json:"studyTime"`
}
