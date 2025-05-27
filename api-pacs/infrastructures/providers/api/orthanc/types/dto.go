package types

type JobStatus string
type UploadDICOMStatus string

const (
	// job status
	JobPending JobStatus = "Pending"
	JobRunning JobStatus = "Running"
	JobSuccess JobStatus = "Success"
	JobFailure JobStatus = "Failure"
	JobPaused  JobStatus = "Paused"
	JobRetry   JobStatus = "Retry"
	// upload dicom status
	UploadDICOMStatusSuccess       UploadDICOMStatus = "Success"
	UploadDICOMStatusAlreadyStored UploadDICOMStatus = "AlreadyStored"
	UploadDICOMStatusFailure       UploadDICOMStatus = "Failure"
	UploadDICOMStatusFilteredOut   UploadDICOMStatus = "FilteredOut"
)

type DeleteLocalResourcesRequest struct {
	Resources []string `json:"Resources"`
}

type GetDICOMModalitiesRequest struct {
	Expand bool `json:"Expand"`
}

type QueryModalitiesRequest struct {
	Level     string     `json:"Level"`
	LocalAET  string     `json:"LocalAet"`
	Normalize bool       `json:"Normalize"`
	Query     QueryStudy `json:"Query"`
	Timeout   uint       `json:"Timeout"`
}

type QueryLocalResourceRequest struct {
	Level  string             `json:"Level"`
	Query  QueryLocalResource `json:"Query"`
	Expand bool               `json:"Expand,omitempty"`
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

type TriggerDICOMEchoRequest struct {
	CheckFind bool `json:"CheckFind"`
	Timeout   uint `json:"Timeout"`
}

type UpdateDICOMModalityRequest struct {
	AET                    string `json:"AET"`
	AllowEcho              bool   `json:"AllowEcho"`
	AllowFind              bool   `json:"AllowFind"`
	AllowFindWorklist      bool   `json:"AllowFindWorklist"`
	AllowGet               bool   `json:"AllowGet"`
	AllowMove              bool   `json:"AllowMove"`
	AllowStorageCommitment bool   `json:"AllowStorageCommitment"`
	AllowStore             bool   `json:"AllowStore"`
	AllowTranscoding       bool   `json:"AllowTranscoding"`
	Host                   string `json:"Host"`
	Port                   uint   `json:"Port"`
	UseDicomTLS            bool   `json:"UseDicomTls"`
}

type GetJobResponse struct {
	ID       string `json:"ID"`
	Priority uint   `json:"Priority"`
	Progress uint   `json:"Progress"`
	State    string `json:"State"`
}

type GetLocalResourceResponse struct {
	ID            string `json:"ID"`
	LastUpdate    string `json:"LastUpdate"` // in 20240627T182452 format
	MainDICOMTags struct {
		SeriesInstanceUID string `json:"SeriesInstanceUID"`
		SeriesTime        string `json:"SeriesTime"`
	} `json:"MainDicomTags,omitempty"` // for level = series
}

type ListDICOMModalitiesResponse struct {
	AET               string `json:"AET"`
	AllowEcho         bool   `json:"AllowEcho"`
	AllowFind         bool   `json:"AllowFind"`
	AllowFindWorklist bool   `json:"AllowFindWorklist"`
	AllowGet          bool   `json:"AllowGet"`
	AllowMove         bool   `json:"AllowMove"`
	AllowStore        bool   `json:"AllowStore"`
	AllowTranscoding  bool   `json:"AllowTranscoding"`
	Host              string `json:"Host"`
	Port              uint   `json:"Port"`
	Timeout           uint   `json:"Timeout"`
	UseDicomTLS       bool   `json:"UseDicomTls"`
}

type QueryModalityResponse struct {
	ID   string `json:"ID"`
	Path string `json:"Path"`
}

type QueryModalityStudyAnswersResponse struct {
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

type QueryModalitySeriesAnswersResponse struct {
	AccessionNumber      string `json:"AccessionNumber"`
	PatientID            string `json:"PatientID"`
	QueryRetrieveLevel   string `json:"QueryRetrieveLevel"`
	RetrieveAETitle      string `json:"RetrieveAETitle"`
	SeriesInstanceUID    string `json:"SeriesInstanceUID"`
	SpecificCharacterSet string `json:"SpecificCharacterSet"`
	StudyInstanceUID     string `json:"StudyInstanceUID"`
}

type StraightDICOMStoreSCUResponse struct {
	SOPClassUID    string `json:"SOPClassUID"`
	SOPInstanceUID string `json:"SOPInstanceUID"`
}

type UploadDICOMInstancesResponse struct {
	ID            string            `json:"ID"`
	ParentPatient string            `json:"ParentPatient"`
	ParentSeries  string            `json:"ParentSeries"`
	ParentStudy   string            `json:"ParentStudy"`
	Path          string            `json:"Path"`
	Status        UploadDICOMStatus `json:"Status"`
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

type QueryLocalResource struct {
	StudyInstanceUID string `json:"StudyInstanceUID,omitempty"`
}
