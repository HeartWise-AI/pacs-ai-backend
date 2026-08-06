package types

const (
	LegacyBackfillSkipExistingRun     = "existing_run"
	LegacyBackfillSkipTenantMismatch  = "tenant_mismatch"
	LegacyBackfillSkipInvalidIdentity = "invalid_identity"
	LegacyBackfillSkipInvalidStatus   = "invalid_status"
	LegacyBackfillSkipDuplicateModel  = "duplicate_model"
)

// LegacyProcessingRunBackfillDryRun is an identifier-free operational report.
// Map keys come only from the bounded constants above or the processing status enum.
type LegacyProcessingRunBackfillDryRun struct {
	OrphanExecutions   int            `json:"orphanExecutions"`
	Candidates         int            `json:"candidates"`
	StudyGroups        int            `json:"studyGroups"`
	EligibleStudies    int            `json:"eligibleStudies"`
	EligibleExecutions int            `json:"eligibleExecutions"`
	SkippedStudies     int            `json:"skippedStudies"`
	SkippedExecutions  int            `json:"skippedExecutions"`
	EligibleStatuses   map[string]int `json:"eligibleStatuses"`
	SkipReasons        map[string]int `json:"skipReasons"`
}
