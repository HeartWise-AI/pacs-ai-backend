package entity

const (
	CreateAction     string = "CREATE"
	UpdateAction     string = "UPDATE"
	DeleteAction     string = "DELETE"
	SuspendAction    string = "SUSPEND"
	ReactivateAction string = "REACTIVATE"
)

// AdminMember holds the admin member entity fields
type AdminMember struct {
	TenantID      string `json:"tenant_id"`
	TenantName    string `json:"tenant_name"`
	UserID        string `json:"user_id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	Role          string `json:"role"`
	LicenseNo     string `json:"license_no"`
	Specialty     string `json:"specialty"`
	Action        string `json:"action"` // enum
	ActorUserID   string `json:"actor_user_id,omitempty"`
	ActorRole     string `json:"actor_role,omitempty"`
	PreviousState string `json:"previous_state,omitempty"`
	NewState      string `json:"new_state,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	Timestamp     uint   `json:"timestamp"`
}

// GetModelName returns the model name of admin member entity that can be used for naming schemas
func (entity *AdminMember) GetModelName() string {
	return "admin_members"
}
