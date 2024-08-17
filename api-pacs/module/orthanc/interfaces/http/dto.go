package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"FindLocalResourceRequest.Level":                "Level is required",
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
	AccessionNumber            string `json:"accessionNumber"`
	InstitutionName            string `json:"institutionName"`
	ModalitiesInStudy          string `json:"modalitiesInStudy"`
	NumberOfStudyRelatedSeries string `json:"numberOfStudyRelatedSeries"`
	PatientBirthDate           string `json:"patientBirthDate"`
	PatientID                  string `json:"patientID"`
	PatientName                string `json:"patientName"`
	PatientSex                 string `json:"patientSex"`
	ReferringPhysicianName     string `json:"referringPhysicianName"`
	RequestingPhysician        string `json:"requestingPhysician"`
	StudyDate                  string `json:"studyDate"`
	StudyDescription           string `json:"studyDescription"`
	StudyID                    string `json:"studyID"`
	StudyInstanceUID           string `json:"studyInstanceUID"`
	StudyTime                  string `json:"studyTime"`
}

type RetrieveModalityStudyRequest struct {
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
	PatientID                  string `json:"patientID"`
	PatientName                string `json:"patientName"`
	PatientSex                 string `json:"patientSex"`
	QueryRetrieveLevel         string `json:"queryRetrieveLevel"`
	ReferringPhysicianName     string `json:"referringPhysicianName"`
	RetrieveAETitle            string `json:"retrieveAETitle"`
	SpecificCharacterSet       string `json:"specificCharacterSet"`
	StudyDate                  string `json:"studyDate"`
	StudyDescription           string `json:"studyDescription"`
	StudyID                    string `json:"studyID"`
	StudyInstanceUID           string `json:"studyInstanceUID"`
	StudyTime                  string `json:"studyTime"`
}
