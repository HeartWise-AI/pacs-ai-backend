package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

type legacyBackfillGroup struct {
	tenantID         string
	studyInstanceUID string
	rows             []repositoryTypes.LegacyProcessingRunBackfillRow
}

// DryRunLegacyProcessingRunBackfill reads and validates current legacy state
// without creating runs or changing execution rows.
func (service *InferenceQueryService) DryRunLegacyProcessingRunBackfill(ctx context.Context) (types.LegacyProcessingRunBackfillDryRun, error) {
	rows, err := service.InferenceProcessingRunRepositoryInterface.ListLegacyProcessingRunBackfillRows(ctx)
	if err != nil {
		return types.LegacyProcessingRunBackfillDryRun{}, err
	}
	return PlanLegacyProcessingRunBackfill(rows), nil
}

// VerifyLegacyProcessingRunBackfill proves the persisted import against
// operator-approved totals, candidate correlation, and authoritative aggregate rules.
func (service *InferenceQueryService) VerifyLegacyProcessingRunBackfill(ctx context.Context, data types.VerifyLegacyProcessingRunBackfill) (types.LegacyProcessingRunBackfillVerification, error) {
	report := types.LegacyProcessingRunBackfillVerification{
		ExpectedStudies: data.ExpectedStudies, ExpectedExecutions: data.ExpectedExecutions,
		Issues: map[string]int{},
	}
	if data.ExpectedStudies <= 0 || data.ExpectedExecutions <= 0 {
		return report, errors.New(apiError.InvalidPayload)
	}

	snapshot, err := service.InferenceProcessingRunRepositoryInterface.LoadLegacyProcessingRunVerificationSnapshot(ctx)
	if err != nil {
		return report, err
	}
	runs := snapshot.Runs
	executionRows := snapshot.Executions
	report.Remaining = PlanLegacyProcessingRunBackfill(snapshot.Orphans)
	report.ImportedStudies = len(snapshot.Runs)
	report.ImportedExecutions = len(snapshot.Executions)
	if report.ImportedStudies != data.ExpectedStudies {
		report.Issues[types.LegacyBackfillVerifyStudyCount]++
	}
	if report.ImportedExecutions != data.ExpectedExecutions {
		report.Issues[types.LegacyBackfillVerifyExecutionCount]++
	}
	if report.Remaining.OrphanExecutions != 0 {
		report.Issues[types.LegacyBackfillVerifyRemainingOrphans]++
	}

	runsByID := make(map[string]entity.InferenceIngestionProcessingRun, len(runs))
	executionsByRun := make(map[string][]repositoryTypes.LegacyProcessingRunVerificationExecution, len(runs))
	for _, run := range runs {
		runsByID[run.ID] = run
	}
	for _, execution := range executionRows {
		if execution.ProcessingRunID == nil {
			report.Issues[types.LegacyBackfillVerifyUnknownRun]++
			continue
		}
		if _, exists := runsByID[*execution.ProcessingRunID]; !exists {
			report.Issues[types.LegacyBackfillVerifyUnknownRun]++
			continue
		}
		executionsByRun[*execution.ProcessingRunID] = append(executionsByRun[*execution.ProcessingRunID], execution)
	}

	for _, run := range runs {
		rows := executionsByRun[run.ID]
		invalid := false
		mark := func(reason string) {
			report.Issues[reason]++
			invalid = true
		}
		if len(rows) == 0 {
			mark(types.LegacyBackfillVerifyEmptyRun)
		}
		if run.RunNumber != 1 {
			mark(types.LegacyBackfillVerifyInvalidRunNumber)
		}
		if run.Version < 1 {
			mark(types.LegacyBackfillVerifyInvalidVersion)
		}

		models := make(map[string]struct{}, len(rows))
		executions := make([]entity.InferenceIngestionProcessingJob, 0, len(rows))
		for _, row := range rows {
			execution := row.InferenceIngestionProcessingJob
			executions = append(executions, execution)
			if strings.TrimSpace(execution.TenantID) != strings.TrimSpace(run.TenantID) ||
				strings.TrimSpace(row.CandidateTenantID) != strings.TrimSpace(run.TenantID) {
				mark(types.LegacyBackfillVerifyTenantMismatch)
			}
			if strings.TrimSpace(row.CandidateStudyInstanceUID) != strings.TrimSpace(run.StudyInstanceUID) {
				mark(types.LegacyBackfillVerifyStudyMismatch)
			}
			model := strings.ToLower(strings.TrimSpace(execution.ModelName))
			if _, duplicate := models[model]; duplicate {
				mark(types.LegacyBackfillVerifyDuplicateModel)
			}
			models[model] = struct{}{}
		}

		aggregate := entity.AggregateInferenceIngestionProcessingRun(entity.InferenceIngestionProcessingRunAggregationInput{
			Run: run, Executions: executions, WholeRunCancelled: verificationExecutionsAllCancelled(executions),
		})
		if !legacyVerificationAggregateMatches(run, aggregate) {
			mark(types.LegacyBackfillVerifyAggregateMismatch)
		}
		if invalid {
			report.InvalidRuns++
		}
	}

	report.Passed = len(report.Issues) == 0
	return report, nil
}

func verificationExecutionsAllCancelled(executions []entity.InferenceIngestionProcessingJob) bool {
	if len(executions) == 0 {
		return false
	}
	for _, execution := range executions {
		if execution.Status != entity.InferenceIngestionProcessingJobStatusCancelled {
			return false
		}
	}
	return true
}

func legacyVerificationAggregateMatches(run entity.InferenceIngestionProcessingRun, aggregate entity.InferenceIngestionProcessingRunAggregation) bool {
	return run.Phase == aggregate.Phase &&
		equalLegacyVerificationOutcome(run.Outcome, aggregate.Outcome) &&
		run.AttentionRequired == aggregate.AttentionRequired &&
		equalLegacyVerificationReasons(run.AttentionReasons, aggregate.AttentionReasons) &&
		equalLegacyVerificationTime(run.StartedAt, aggregate.StartedAt) &&
		equalLegacyVerificationTime(run.CompletedAt, aggregate.CompletedAt)
}

func equalLegacyVerificationOutcome(left, right *entity.InferenceIngestionProcessingRunOutcome) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func equalLegacyVerificationReasons(left, right entity.InferenceIngestionProcessingRunAttentionReasons) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Code != right[index].Code || !equalLegacyVerificationString(left[index].Message, right[index].Message) {
			return false
		}
	}
	return true
}

func equalLegacyVerificationString(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func equalLegacyVerificationTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Equal(*right)
}

// PlanLegacyProcessingRunBackfill groups the minimal repository projection and
// produces an identifier-free, bounded report suitable for logs and rollout gates.
func PlanLegacyProcessingRunBackfill(rows []repositoryTypes.LegacyProcessingRunBackfillRow) types.LegacyProcessingRunBackfillDryRun {
	report := types.LegacyProcessingRunBackfillDryRun{
		OrphanExecutions: len(rows),
		EligibleStatuses: make(map[string]int),
		SkipReasons:      make(map[string]int),
	}
	candidates := make(map[string]struct{})
	groups := make(map[string]*legacyBackfillGroup)
	for _, row := range rows {
		if candidateID := strings.TrimSpace(row.CandidateID); candidateID != "" {
			candidates[candidateID] = struct{}{}
		}
		key := legacyBackfillGroupKey(row)
		group := groups[key]
		if group == nil {
			group = &legacyBackfillGroup{}
			groups[key] = group
		}
		group.rows = append(group.rows, row)
	}

	report.Candidates = len(candidates)
	report.StudyGroups = len(groups)
	for _, group := range groups {
		if reason := legacyBackfillGroupSkipReason(group.rows); reason != "" {
			report.SkippedStudies++
			report.SkippedExecutions += len(group.rows)
			report.SkipReasons[reason]++
			continue
		}
		report.EligibleStudies++
		report.EligibleExecutions += len(group.rows)
		for _, row := range group.rows {
			status, _ := entity.ParseInferenceIngestionProcessingJobStatus(string(row.Status))
			report.EligibleStatuses[string(status)]++
		}
	}
	return report
}

// ApplyLegacyProcessingRunBackfill re-runs preflight immediately before any
// write, validates explicit operator expectations, then imports one study per
// transaction in deterministic order.
func (service *InferenceCommandService) ApplyLegacyProcessingRunBackfill(ctx context.Context, data types.ApplyLegacyProcessingRunBackfill) (types.LegacyProcessingRunBackfillApplyResult, error) {
	result := types.LegacyProcessingRunBackfillApplyResult{Outcomes: map[string]int{}}
	if data.Confirmation != types.LegacyBackfillConfirmation || data.ExpectedStudies <= 0 || data.ExpectedExecutions <= 0 {
		return result, errors.New(apiError.InvalidPayload)
	}

	rows, err := service.InferenceProcessingRunRepositoryInterface.ListLegacyProcessingRunBackfillRows(ctx)
	if err != nil {
		return result, err
	}
	result.Plan = PlanLegacyProcessingRunBackfill(rows)
	if result.Plan.SkippedStudies != 0 ||
		result.Plan.EligibleStudies != data.ExpectedStudies ||
		result.Plan.EligibleExecutions != data.ExpectedExecutions {
		return result, errors.New(apiError.InvalidPayload)
	}

	for _, group := range eligibleLegacyBackfillGroups(rows) {
		imported, importErr := service.InferenceProcessingRunRepositoryInterface.ImportLegacyProcessingRun(ctx, repositoryTypes.ImportLegacyProcessingRun{
			RunID:              generateID(),
			TenantID:           group.tenantID,
			StudyInstanceUID:   group.studyInstanceUID,
			ExpectedExecutions: len(group.rows),
		})
		if importErr == nil {
			result.ImportedStudies++
			result.ImportedExecutions += imported.LinkedExecutions
			result.Outcomes[types.LegacyBackfillOutcomeImported]++
			continue
		}
		if importErr.Error() == apiError.DuplicateRecord || importErr.Error() == apiError.MissingRecord {
			remainingRows, refreshErr := service.InferenceProcessingRunRepositoryInterface.ListLegacyProcessingRunBackfillRows(ctx)
			if refreshErr != nil {
				result.Outcomes[types.LegacyBackfillOutcomeFailed]++
				return result, refreshErr
			}
			if !legacyBackfillGroupRemains(remainingRows, group.tenantID, group.studyInstanceUID) {
				result.AlreadyImportedStudies++
				result.AlreadyImportedExecutions += len(group.rows)
				result.Outcomes[types.LegacyBackfillOutcomeAlreadyDone]++
				continue
			}
		}
		result.Outcomes[types.LegacyBackfillOutcomeFailed]++
		return result, importErr
	}
	return result, nil
}

// RollbackLegacyProcessingRunBackfill is the guarded incident escape hatch.
// It delegates each run to a transaction that unlinks before deleting.
func (service *InferenceCommandService) RollbackLegacyProcessingRunBackfill(ctx context.Context, data types.RollbackLegacyProcessingRunBackfill) (types.LegacyProcessingRunBackfillRollbackResult, error) {
	result := types.LegacyProcessingRunBackfillRollbackResult{Outcomes: map[string]int{}}
	if data.Confirmation != types.LegacyBackfillRollbackConfirmation || data.ExpectedStudies <= 0 || data.ExpectedExecutions <= 0 {
		return result, errors.New(apiError.InvalidPayload)
	}
	snapshot, err := service.InferenceProcessingRunRepositoryInterface.LoadLegacyProcessingRunVerificationSnapshot(ctx)
	if err != nil {
		return result, err
	}
	result.PlannedStudies = len(snapshot.Runs)
	result.PlannedExecutions = len(snapshot.Executions)
	if result.PlannedStudies != data.ExpectedStudies || result.PlannedExecutions != data.ExpectedExecutions {
		return result, errors.New(apiError.InvalidPayload)
	}

	executionCounts := make(map[string]int, len(snapshot.Runs))
	for _, execution := range snapshot.Executions {
		if execution.ProcessingRunID == nil {
			return result, errors.New(apiError.InvalidPayload)
		}
		executionCounts[*execution.ProcessingRunID]++
	}
	runs := append([]entity.InferenceIngestionProcessingRun(nil), snapshot.Runs...)
	sort.Slice(runs, func(i, j int) bool {
		left := runs[i].TenantID + "\x00" + runs[i].StudyInstanceUID + "\x00" + runs[i].ID
		right := runs[j].TenantID + "\x00" + runs[j].StudyInstanceUID + "\x00" + runs[j].ID
		return left < right
	})
	for _, run := range runs {
		expected := executionCounts[run.ID]
		if expected <= 0 {
			result.Outcomes[types.LegacyBackfillRollbackOutcomeFailed]++
			return result, errors.New(apiError.InvalidPayload)
		}
		reverted, rollbackErr := service.InferenceProcessingRunRepositoryInterface.RollbackLegacyProcessingRun(ctx, repositoryTypes.RollbackLegacyProcessingRun{
			RunID: run.ID, ExpectedExecutions: expected,
		})
		if rollbackErr == nil {
			result.RevertedStudies++
			result.RevertedExecutions += reverted.UnlinkedExecutions
			result.Outcomes[types.LegacyBackfillRollbackOutcomeReverted]++
			continue
		}
		if rollbackErr.Error() == apiError.MissingRecord {
			fresh, refreshErr := service.InferenceProcessingRunRepositoryInterface.LoadLegacyProcessingRunVerificationSnapshot(ctx)
			if refreshErr != nil {
				result.Outcomes[types.LegacyBackfillRollbackOutcomeFailed]++
				return result, refreshErr
			}
			if !legacyImportedRunExists(fresh.Runs, run.ID) {
				result.AlreadyRevertedStudies++
				result.AlreadyRevertedExecutions += expected
				result.Outcomes[types.LegacyBackfillRollbackOutcomeAlready]++
				continue
			}
		}
		result.Outcomes[types.LegacyBackfillRollbackOutcomeFailed]++
		return result, rollbackErr
	}
	return result, nil
}

func legacyImportedRunExists(runs []entity.InferenceIngestionProcessingRun, runID string) bool {
	for _, run := range runs {
		if run.ID == runID {
			return true
		}
	}
	return false
}

func legacyBackfillGroupRemains(rows []repositoryTypes.LegacyProcessingRunBackfillRow, tenantID, studyInstanceUID string) bool {
	wantedKey := strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(studyInstanceUID)
	for _, row := range rows {
		if legacyBackfillGroupKey(row) == wantedKey {
			return true
		}
	}
	return false
}

func eligibleLegacyBackfillGroups(rows []repositoryTypes.LegacyProcessingRunBackfillRow) []*legacyBackfillGroup {
	groupsByKey := make(map[string]*legacyBackfillGroup)
	for _, row := range rows {
		key := legacyBackfillGroupKey(row)
		group := groupsByKey[key]
		if group == nil {
			group = &legacyBackfillGroup{
				tenantID:         strings.TrimSpace(row.ExecutionTenantID),
				studyInstanceUID: strings.TrimSpace(row.StudyInstanceUID),
			}
			groupsByKey[key] = group
		}
		group.rows = append(group.rows, row)
	}

	keys := make([]string, 0, len(groupsByKey))
	for key, group := range groupsByKey {
		if legacyBackfillGroupSkipReason(group.rows) == "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	groups := make([]*legacyBackfillGroup, 0, len(keys))
	for _, key := range keys {
		groups = append(groups, groupsByKey[key])
	}
	return groups
}

func legacyBackfillGroupKey(row repositoryTypes.LegacyProcessingRunBackfillRow) string {
	tenantID := strings.TrimSpace(row.ExecutionTenantID)
	studyInstanceUID := strings.TrimSpace(row.StudyInstanceUID)
	if tenantID == "" || studyInstanceUID == "" {
		return "invalid\x00" + strings.TrimSpace(row.ExecutionID)
	}
	return tenantID + "\x00" + studyInstanceUID
}

func legacyBackfillGroupSkipReason(rows []repositoryTypes.LegacyProcessingRunBackfillRow) string {
	for _, row := range rows {
		if row.ExistingRun {
			return types.LegacyBackfillSkipExistingRun
		}
	}
	for _, row := range rows {
		if strings.TrimSpace(row.ExecutionTenantID) != strings.TrimSpace(row.CandidateTenantID) {
			return types.LegacyBackfillSkipTenantMismatch
		}
	}
	for _, row := range rows {
		if strings.TrimSpace(row.ExecutionID) == "" ||
			strings.TrimSpace(row.CandidateID) == "" ||
			strings.TrimSpace(row.ExecutionTenantID) == "" ||
			strings.TrimSpace(row.CandidateTenantID) == "" ||
			strings.TrimSpace(row.StudyInstanceUID) == "" ||
			strings.TrimSpace(row.ModelName) == "" {
			return types.LegacyBackfillSkipInvalidIdentity
		}
	}
	for _, row := range rows {
		if _, valid := entity.ParseInferenceIngestionProcessingJobStatus(string(row.Status)); !valid {
			return types.LegacyBackfillSkipInvalidStatus
		}
	}
	models := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		modelName := strings.ToLower(strings.TrimSpace(row.ModelName))
		if _, duplicate := models[modelName]; duplicate {
			return types.LegacyBackfillSkipDuplicateModel
		}
		models[modelName] = struct{}{}
	}
	return ""
}
