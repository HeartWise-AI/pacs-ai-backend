package types

// RunLabel is one label on a model run. Label is the text as typed; Key is
// its case-insensitive form, used to match and to query.
type RunLabel struct {
	Label     string  `json:"label"`
	Key       string  `json:"key"`
	Source    string  `json:"source"`
	CreatedBy *string `json:"createdBy"`
	CreatedAt *string `json:"createdAt"`
}

// StudyServiceJobLabels is study-service's GET/POST/DELETE /jobs/{id}/labels body.
type StudyServiceJobLabels struct {
	JobID            string     `json:"jobId"`
	StudyInstanceUID string     `json:"studyInstanceUID"`
	ModelName        string     `json:"modelName"`
	ModelVersion     *string    `json:"modelVersion"`
	Modality         string     `json:"modality"`
	Labels           []RunLabel `json:"labels"`
}

// RunLabels is the viewer's view of the labels of one run.
type RunLabels struct {
	StudyInstanceUID  string     `json:"studyInstanceUID"`
	StudyServiceJobID string     `json:"studyServiceJobId"`
	ModelName         string     `json:"modelName"`
	Modality          string     `json:"modality"`
	Labels            []RunLabel `json:"labels"`
}

// RunLabelCatalogEntry is a label the tenant has used, with the number of
// runs that carry it now.
type RunLabelCatalogEntry struct {
	Label      string  `json:"label"`
	Key        string  `json:"key"`
	RunCount   int     `json:"runCount"`
	LastUsedAt *string `json:"lastUsedAt"`
}

// RunLabelCatalog lists the tenant's labels, most recently used first. It
// only feeds suggestions: any text can be a label.
type RunLabelCatalog struct {
	Labels []RunLabelCatalogEntry `json:"labels"`
}

// GetRunLabels scopes a label read to one tenant study and study-service job.
type GetRunLabels struct {
	TenantID          string
	StudyInstanceUID  string
	StudyServiceJobID string
	// AllowModel, when set, must accept the run's model-info modelId.
	AllowModel func(modelID string) bool
}

// ChangeRunLabel adds or removes one label of a run on behalf of UserID.
type ChangeRunLabel struct {
	GetRunLabels
	UserID string
	Label  string
}
