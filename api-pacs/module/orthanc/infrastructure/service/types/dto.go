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

type RetrieveModalityStudyBySeries struct {
	TenantID         string
	UserID           string
	ModalityID       string
	StudyInstanceUID string
	ModalityType     string
}

type UpdateDICOMModality struct {
	ModalityID  string
	AET         string
	Host        string
	Port        uint
	UseDicomTLS bool
}
