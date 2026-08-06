package service

import (
	"context"
	"errors"
	"sort"
	"strings"

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
