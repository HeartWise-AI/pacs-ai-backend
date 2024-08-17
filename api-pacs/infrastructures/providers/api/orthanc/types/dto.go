package types

const (
	JobPending string = "Pending"
	JobRunning string = "Running"
	JobSuccess string = "Success"
	JobFailure string = "Failure"
	JobPaused  string = "Paused"
	JobRetry   string = "Retry"
)

type DeleteLocalResourcesRequest struct {
	Resources []string `json:"Resources"`
}

type RetrieveQueryModalityAnswerRequest struct {
	Asynchronous bool   `json:"Asynchronous"`
	Full         bool   `json:"Full"`
	Permissive   bool   `json:"Permissive"`
	Priority     uint   `json:"Priority"`
	Simplify     bool   `json:"Simplify"`
	Synchronous  bool   `json:"Synchronous"`
	TargetAet    string `json:"TargetAet"`
	Timeout      uint   `json:"Timeout"`
}

type QueryModalitiesRequest struct {
	Level     string     `json:"Level"`
	LocalAET  string     `json:"LocalAet"`
	Normalize bool       `json:"Normalize"`
	Query     QueryStudy `json:"Query"`
	Timeout   uint       `json:"Timeout"`
}

type QueryLocalStudyRequest struct {
	Level string          `json:"Level"`
	Query QueryLocalStudy `json:"Query"`
}

type GetJobResponse struct {
	ID       string `json:"ID"`
	Priority uint   `json:"Priority"`
	Progress uint   `json:"Progress"`
	State    string `json:"State"`
}

type GetLocalResourceResponse struct {
	ID         string `json:"ID"`
	LastUpdate string `json:"LastUpdate"` // in 20240627T182452 format
}

type RetrieveQueryModalityAnswerResponse struct {
	ID   string `json:"ID"`
	Path string `json:"Path"`
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

type QueryLocalStudy struct {
	StudyInstanceUID string `json:"StudyInstanceUID"`
}

// CreateFindQueryRequest represents the request for creating a find query
type CreateFindQueryRequest struct {
	Level string
	Query QueryInstance
}

// QueryInstance represents the query parameters for an instance
type QueryInstance struct {
	SOPInstanceUID string `json:"SOPInstanceUID,omitempty"`
}
