package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"FindLocalResourceRequest.Level":                "Level is required",
		"FindModalityStudiesRequest.ModalityID":         "Modality ID is required",
		"RetrieveModalityStudyRequest.ModalityID":       "Modality ID is required",
		"RetrieveModalityStudyRequest.StudyInstanceUID": "Study instance uid is required.",
	}
)

type FindLocalResourceRequest struct {
	Level string `json:"level" validate:"required"`
	Query struct {
		StudyInstanceUID string `json:"studyInstanceUID,omitempty"`
		SOPInstanceUID   string `json:"sopInstanceUID,omitempty"`
	} `json:"query"`
}

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

type FindLocalResourceResponse struct {
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

type RetrieveQueryModalityAnswerResponse struct {
	ID   string `json:"id"`
	Path string `json:"path"`
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
