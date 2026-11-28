package types

import (
	"time"

	"api-pacs/module/inference/domain/entity"
)

// PredictionsView is one of the three AI Predictions views.
type PredictionsView string

const (
	// PredictionsViewMine: exams where the current user launched at least one run.
	PredictionsViewMine PredictionsView = "mine"
	// PredictionsViewAutomatic: exams with at least one automatic run.
	PredictionsViewAutomatic PredictionsView = "automatic"
	// PredictionsViewAll: every exam; workspace administrators (ADMIN, OWNER) only.
	PredictionsViewAll PredictionsView = "all"
)

// PredictionState is the state shown for one model execution.
type PredictionState string

const (
	// PredictionStateWaiting: not dispatched because the exam is still receiving images.
	PredictionStateWaiting PredictionState = "waiting"
	PredictionStateQueued  PredictionState = "queued"
	// PredictionStateStalled: never ran. The execution is still pending or
	// queued without a study-service job long after its run was created; the
	// dispatcher will not pick it up again, so it needs to be closed.
	PredictionStateStalled PredictionState = "stalled"
	// PredictionStateExpired: an automatic execution that did not complete and
	// was closed by the expiry of automatic runs (INFERENCE_AUTO_RUN_EXPIRE_AFTER_HOURS).
	PredictionStateExpired   PredictionState = "expired"
	PredictionStateRunning   PredictionState = "running"
	PredictionStateCompleted PredictionState = "completed"
	PredictionStateFailed    PredictionState = "failed"
	PredictionStateSkipped   PredictionState = "skipped"
	PredictionStateCancelled PredictionState = "cancelled"
)

// ListPredictions is an AI Predictions list or export request. The tenant,
// user and role come from the authenticated request, never from the query.
type ListPredictions struct {
	TenantID string
	UserID   string
	Role     string
	// CanViewAll is true when the session holds results.view_all.
	CanViewAll bool
	View       PredictionsView
	Search     string
	Model      string
	Status     string
	Modality   string
	From       *time.Time
	To         *time.Time
	// LaunchedBy is accepted in the "all" view only: "" or "any",
	// "automatic", "people", or "user:<user id>".
	LaunchedBy string
	Limit      int
	Offset     int
	// AllowModelName, when set, must accept a line's model name for the export
	// to write it (research-only models withheld from the session's access).
	AllowModelName func(modelName string) bool
}

// GetPredictionModelHistory pages the executions of one model on one exam.
type GetPredictionModelHistory struct {
	TenantID         string
	UserID           string
	StudyInstanceUID string
	Model            string
	Limit            int
	Offset           int
}

// PredictionExecution is one model execution as the page shows it.
type PredictionExecution struct {
	RunID       string                                        `json:"runId"`
	ExecutionID string                                        `json:"executionId"`
	RunNumber   int                                           `json:"runNumber"`
	Trigger     entity.InferenceIngestionProcessingRunTrigger `json:"trigger"`
	// RequestedByUserID is null for automatic runs.
	RequestedByUserID *string `json:"requestedByUserId"`
	// RequestedByName is null when the user is unknown or the lookup failed.
	RequestedByName *string                                           `json:"requestedByName"`
	RequestedByYou  bool                                              `json:"requestedByYou"`
	ModelName       string                                            `json:"modelName"`
	ModelVersion    *string                                           `json:"modelVersion"`
	Modality        *string                                           `json:"modality"`
	State           PredictionState                                   `json:"state"`
	Status          entity.InferenceIngestionProcessingJobStatus      `json:"status"`
	ErrorMessage    *string                                           `json:"errorMessage"`
	SkipReason      *entity.InferenceIngestionProcessingJobSkipReason `json:"skipReason"`
	// Cancellable is true while the execution has not started computing; the
	// cancel endpoint cancels every such execution of the run.
	Cancellable     bool       `json:"cancellable"`
	CancelledAt     *time.Time `json:"cancelledAt"`
	RunCreatedAt    time.Time  `json:"runCreatedAt"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	StartedAt       *time.Time `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt"`
	DurationSeconds *float64   `json:"durationSeconds"`
}

// PredictionModel is the latest execution of one model on an exam.
type PredictionModel struct {
	PredictionExecution
	// EarlierRuns is the number of older executions of this model on the exam.
	EarlierRuns int `json:"earlierRuns"`
}

// PredictionCounts counts the latest state of each model of an exam.
type PredictionCounts struct {
	Total     int `json:"total"`
	Waiting   int `json:"waiting"`
	Queued    int `json:"queued"`
	Stalled   int `json:"stalled"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	Cancelled int `json:"cancelled"`
	Expired   int `json:"expired"`
}

// PredictionLauncher counts the runs one launcher started on an exam.
type PredictionLauncher struct {
	// Kind is "automatic" or "user".
	Kind    string                                        `json:"kind"`
	Trigger entity.InferenceIngestionProcessingRunTrigger `json:"trigger"`
	UserID  *string                                       `json:"userId"`
	Name    *string                                       `json:"name"`
	You     bool                                          `json:"you"`
	Runs    int                                           `json:"runs"`
	FirstAt time.Time                                     `json:"firstAt"`
}

// PredictionExam is one row of the AI Predictions page. The gateway does not
// store patient names; clients resolve them from the local DICOMweb store.
type PredictionExam struct {
	StudyInstanceUID  string    `json:"studyInstanceUID"`
	PatientID         *string   `json:"patientId"`
	AccessionNumber   *string   `json:"accessionNumber"`
	ModalitiesInStudy *string   `json:"modalitiesInStudy"`
	StudyDate         *string   `json:"studyDate"`
	LastActivityAt    time.Time `json:"lastActivityAt"`
	// Active is true while a model is waiting, queued or running; stalled
	// models do not count.
	Active    bool                 `json:"active"`
	Counts    PredictionCounts     `json:"counts"`
	Models    []PredictionModel    `json:"models"`
	Launchers []PredictionLauncher `json:"launchers"`
}

// PredictionsPage is one page of exams.
type PredictionsPage struct {
	View    PredictionsView  `json:"view"`
	Exams   []PredictionExam `json:"exams"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
	Total   int              `json:"total"`
	HasMore bool             `json:"hasMore"`
	// CanViewAll tells the client whether the "all" view is available.
	CanViewAll bool `json:"canViewAll"`
}

// PredictionHistoryPage is one page of executions of one model on one exam, newest first.
type PredictionHistoryPage struct {
	StudyInstanceUID string                `json:"studyInstanceUID"`
	ModelName        string                `json:"modelName"`
	Executions       []PredictionExecution `json:"executions"`
	Limit            int                   `json:"limit"`
	Offset           int                   `json:"offset"`
	HasMore          bool                  `json:"hasMore"`
}

// PredictionsExportSummary describes a written CSV export.
type PredictionsExportSummary struct {
	Rows      int
	Truncated bool
}
