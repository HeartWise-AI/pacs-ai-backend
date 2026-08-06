package types

const (
	LegacyBackfillSkipExistingRun     = "existing_run"
	LegacyBackfillSkipTenantMismatch  = "tenant_mismatch"
	LegacyBackfillSkipInvalidIdentity = "invalid_identity"
	LegacyBackfillSkipInvalidStatus   = "invalid_status"
	LegacyBackfillSkipDuplicateModel  = "duplicate_model"
	LegacyBackfillConfirmation        = "LEGACY_IMPORT"
	LegacyBackfillOutcomeImported     = "imported"
	LegacyBackfillOutcomeAlreadyDone  = "already_imported"
	LegacyBackfillOutcomeFailed       = "failed"
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

// ApplyLegacyProcessingRunBackfill contains the operator's confirmation of a
// freshly observed, identifier-free dry-run plan.
type ApplyLegacyProcessingRunBackfill struct {
	Confirmation       string
	ExpectedStudies    int
	ExpectedExecutions int
}

// LegacyProcessingRunBackfillApplyResult is deliberately identifier-free so
// it is safe to retain as an operational rollout record.
type LegacyProcessingRunBackfillApplyResult struct {
	Plan                      LegacyProcessingRunBackfillDryRun `json:"plan"`
	ImportedStudies           int                               `json:"importedStudies"`
	ImportedExecutions        int                               `json:"importedExecutions"`
	AlreadyImportedStudies    int                               `json:"alreadyImportedStudies"`
	AlreadyImportedExecutions int                               `json:"alreadyImportedExecutions"`
	Outcomes                  map[string]int                    `json:"outcomes"`
}
