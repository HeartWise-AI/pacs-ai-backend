package types

type QueryModalitiesRequest struct {
	Level     string     `json:"Level"`
	LocalAET  string     `json:"LocalAet"`
	Normalize bool       `json:"Normalize"`
	Query     QueryStudy `json:"Query"`
	Timeout   uint       `json:"Timeout"`
}

type QueryModalitiesResponse struct {
	ID   string `json:"ID"`
	Path string `json:"Path"`
}

type QueryModalitiesAnswersResponse struct {
	AccessionNumber            string `json:"AccessionNumber"`
	ModalitiesInStudy          string `json:"ModalitiesInStudy"`
	NumberOfStudyRelatedSeries string `json:"NumberOfStudyRelatedSeries"`
	PatientBirthDate           string `json:"PatientBirthDate"`
	PatientID                  string `json:"PatientID"`
	PatientName                string `json:"PatientName"`
	PatientSex                 string `json:"PatientSex"`
	QueryRetrieveLevel         string `json:"QueryRetrieveLevel"`
	ReferringPhysicianName     string `json:"ReferringPhysicianName"`
	RetrieveAETitle            string `json:"RetrieveAETitle"`
	SpecificCharacterSet       string `json:"SpecificCharacterSet"`
	StudyDate                  string `json:"StudyDate"`
	StudyDescription           string `json:"StudyDescription"`
	StudyID                    string `json:"StudyID"`
	StudyInstanceUID           string `json:"StudyInstanceUID"`
	StudyTime                  string `json:"StudyTime"`
}

type QueryStudy struct {
	AccessionNumber            string `json:"AccessionNumber"`
	InstitutionName            string `json:"InstitutionName"`
	ModalitiesInStudy          string `json:"ModalitiesInStudy"`
	NumberOfStudyRelatedSeries string `json:"NumberOfStudyRelatedSeries"`
	PatientBirthDate           string `json:"PatientBirthDate"`
	PatientID                  string `json:"PatientID"`
	PatientName                string `json:"PatientName"`
	PatientSex                 string `json:"PatientSex"`
	ReferringPhysicianName     string `json:"ReferringPhysicianName"`
	RequestingPhysician        string `json:"RequestingPhysician"`
	StudyDate                  string `json:"StudyDate"`
	StudyDescription           string `json:"StudyDescription"`
	StudyID                    string `json:"StudyID"`
	StudyInstanceUID           string `json:"StudyInstanceUID"`
	StudyTime                  string `json:"StudyTime"`
}
