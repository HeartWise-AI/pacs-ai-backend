package service

import (
	"context"
	"strings"

	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

type legacyBackfillGroup struct {
	rows []repositoryTypes.LegacyProcessingRunBackfillRow
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
