package types

const (
	LegacyBackfillSkipExistingRun         = "existing_run"
	LegacyBackfillSkipTenantMismatch      = "tenant_mismatch"
	LegacyBackfillSkipInvalidIdentity     = "invalid_identity"
	LegacyBackfillSkipInvalidStatus       = "invalid_status"
	LegacyBackfillSkipDuplicateModel      = "duplicate_model"
	LegacyBackfillConfirmation            = "LEGACY_IMPORT"
	LegacyBackfillOutcomeImported         = "imported"
	LegacyBackfillOutcomeAlreadyDone      = "already_imported"
	LegacyBackfillOutcomeFailed           = "failed"
	LegacyBackfillVerifyStudyCount        = "study_count_mismatch"
	LegacyBackfillVerifyExecutionCount    = "execution_count_mismatch"
	LegacyBackfillVerifyRemainingOrphans  = "remaining_orphans"
	LegacyBackfillVerifyEmptyRun          = "empty_run"
	LegacyBackfillVerifyInvalidRunNumber  = "invalid_run_number"
	LegacyBackfillVerifyInvalidVersion    = "invalid_version"
	LegacyBackfillVerifyTenantMismatch    = "tenant_mismatch"
	LegacyBackfillVerifyStudyMismatch     = "study_mismatch"
	LegacyBackfillVerifyDuplicateModel    = "duplicate_model"
	LegacyBackfillVerifyAggregateMismatch = "aggregate_mismatch"
	LegacyBackfillVerifyUnknownRun        = "unknown_run"
	LegacyBackfillRollbackConfirmation    = "ROLLBACK_LEGACY_IMPORT"
	LegacyBackfillRollbackOutcomeReverted = "reverted"
	LegacyBackfillRollbackOutcomeAlready  = "already_reverted"
	LegacyBackfillRollbackOutcomeFailed   = "failed"
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

type VerifyLegacyProcessingRunBackfill struct {
	ExpectedStudies    int
	ExpectedExecutions int
}

// LegacyProcessingRunBackfillVerification is an identifier-free proof of the
// persisted legacy import and any remaining orphan state.
type LegacyProcessingRunBackfillVerification struct {
	Passed             bool                              `json:"passed"`
	ExpectedStudies    int                               `json:"expectedStudies"`
	ExpectedExecutions int                               `json:"expectedExecutions"`
	ImportedStudies    int                               `json:"importedStudies"`
	ImportedExecutions int                               `json:"importedExecutions"`
	InvalidRuns        int                               `json:"invalidRuns"`
	Remaining          LegacyProcessingRunBackfillDryRun `json:"remaining"`
	Issues             map[string]int                    `json:"issues"`
}

type RollbackLegacyProcessingRunBackfill struct {
	Confirmation       string
	ExpectedStudies    int
	ExpectedExecutions int
}

type LegacyProcessingRunBackfillRollbackResult struct {
	PlannedStudies            int            `json:"plannedStudies"`
	PlannedExecutions         int            `json:"plannedExecutions"`
	RevertedStudies           int            `json:"revertedStudies"`
	RevertedExecutions        int            `json:"revertedExecutions"`
	AlreadyRevertedStudies    int            `json:"alreadyRevertedStudies"`
	AlreadyRevertedExecutions int            `json:"alreadyRevertedExecutions"`
	Outcomes                  map[string]int `json:"outcomes"`
}
