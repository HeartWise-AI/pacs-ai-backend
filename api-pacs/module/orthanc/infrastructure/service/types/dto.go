package types

type FindModalityStudies struct {
	TenantID                   string
	ModalityID                 string
	UserID                     string
	AccessionNumber            string
	InstitutionName            string
	ModalitiesInStudy          string
	NumberOfStudyRelatedSeries string
	PatientBirthDate           string
	PatientID                  string
	PatientName                string
	PatientSex                 string
	ReferringPhysicianName     string
	RequestingPhysician        string
	StudyDate                  string
	StudyDescription           string
	StudyID                    string
	StudyInstanceUID           string
	StudyTime                  string
}

type ListDICOMModalityResult struct {
	AET                 string
	AllowEcho           bool
	AllowFind           bool
	AllowFindWorklist   bool
	AllowGet            bool
	AllowMove           bool
	AllowStore          bool
	AllowTranscoding    bool
	Host                string
	Port                uint
	Timeout             uint
	UseDicomTLS         bool
	TargetCFindEnabled  bool
	TargetCMoveEnabled  bool
	TargetCStoreEnabled bool
}

type RetrieveModalityStudyBySeries struct {
	TenantID         string
	UserID           string
	ModalityID       string
	StudyInstanceUID string
}

type StoreStudyCustomSeries struct {
	TenantID           string
	UserID             string
	ModalityID         string
	StudyInstanceUID   string
	SeriesInstanceUIDs []string
	ModelName          string
	ModelVersion       string
	FileBody           []byte
	FileMimeType       string
}

type UpdateDICOMModality struct {
	TenantID      string
	ModalityID    string
	AET           string
	Host          string
	Port          uint
	UseDicomTLS   bool
	CFindEnabled  bool
	CMoveEnabled  bool
	CStoreEnabled bool
}
