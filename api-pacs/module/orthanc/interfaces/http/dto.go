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
	ID       string `json:"id"`
	Priority uint   `json:"priority"`
	Progress uint   `json:"progress"`
	State    string `json:"state"`
}

type ListDICOMModalitiesResponse struct {
	Modalities map[string]ListDICOMModality `json:"modalities"`
}

type RetrieveQueryModalityAnswerResponse struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type ListDICOMModality struct {
	AET               string `json:"aet"`
	AllowEcho         bool   `json:"allowEcho"`
	AllowFind         bool   `json:"allowFind"`
	AllowFindWorklist bool   `json:"allowFindWorklist"`
	AllowGet          bool   `json:"allowGet"`
	AllowMove         bool   `json:"allowMove"`
	AllowStore        bool   `json:"allowStore"`
	AllowTranscoding  bool   `json:"allowTranscoding"`
	Host              string `json:"host"`
	Port              uint   `json:"port"`
	Timeout           uint   `json:"timeout"`
	UseDicomTLS       bool   `json:"useDicomTLS"`
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
