package types

type CreateAdminMemberLog struct {
	TenantID   string
	TenantName string
	UserID     string
	Email      string
	Name       string
	Role       string
	LicenseNo  string
	Specialty  string
	Action     string
}

type CreateLoginLog struct {
	SessionID  string
	TenantID   string
	TenantName string
	UserID     string
	Email      string
	Name       string
	Role       string
	Specialty  string
}

type CreateGetModalityStudyLog struct {
	TenantID   string
	TenantName string
	ModalityID string
	UserID     string
	Email      string
	Name       string
	QueryID    string
}

type CreateRetrieveStudyLog struct {
	TenantID         string
	TenantName       string
	ModalityID       string
	UserID           string
	Email            string
	Name             string
	StudyInstanceUID string
}

type SearchDocument struct {
	TenantID  string
	Query     string
	StartDate uint
	EndDate   uint
}
