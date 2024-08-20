package types

type FindLocalResource struct {
	Level string
	Query struct {
		StudyInstanceUID string
		SOPInstanceUID   string
	}
}

type FindModalityStudies struct {
	TenantID                   string
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

type RetrieveModalityStudy struct {
	TenantID         string
	UserID           string
	QueryID          string
	AnswerIndex      uint
	StudyInstanceUID string
}
