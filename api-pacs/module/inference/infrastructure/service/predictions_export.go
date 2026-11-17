package service

import (
	"context"
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"

	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

const (
	// predictionsExportBatch is the number of exams read per page while exporting.
	predictionsExportBatch = 200
	// MaximumPredictionsExportExams bounds one export; a larger selection is cut
	// to the most recent exams and reported as truncated before the body starts.
	MaximumPredictionsExportExams = 5000
)

// PredictionsExportHeader is the first CSV line. "Patient name" and "Key
// result" are not stored by the gateway and are left empty; the viewer fills
// them in before saving the file. "Run ID" and "Execution ID" identify the
// line for that and for audits.
var PredictionsExportHeader = []string{
	"Run time", "Study Instance UID", "Accession number", "MRN", "Patient name", "Modality",
	"Model", "Model version", "Trigger", "Launched by", "Status", "Duration (s)", "Key result",
	"Failure or skip reason", "Run ID", "Execution ID",
}

// CSVSafeCell neutralises spreadsheet formulas: a cell starting with = + - @,
// a tab or a carriage return is prefixed with a single quote so that Excel,
// LibreOffice and Google Sheets show it as text (CSV injection).
func CSVSafeCell(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	}
	return value
}

func predictionTriggerLabel(trigger entity.InferenceIngestionProcessingRunTrigger) string {
	switch trigger {
	case entity.InferenceIngestionProcessingRunTriggerAuto:
		return "Automatic"
	case entity.InferenceIngestionProcessingRunTriggerLegacyImport:
		return "Automatic (imported)"
	case entity.InferenceIngestionProcessingRunTriggerViewerRequest:
		return "Viewer"
	case entity.InferenceIngestionProcessingRunTriggerManualReprocess:
		return "Work list reprocess"
	default:
		return string(trigger)
	}
}

func predictionReason(execution types.PredictionExecution) string {
	parts := make([]string, 0, 2)
	if execution.SkipReason != nil {
		reason := string(execution.SkipReason.Code)
		if execution.SkipReason.Message != nil && strings.TrimSpace(*execution.SkipReason.Message) != "" {
			reason += ": " + strings.TrimSpace(*execution.SkipReason.Message)
		}
		parts = append(parts, reason)
	}
	if message := trimmedPointerValue(execution.ErrorMessage); message != "" &&
		(execution.State == types.PredictionStateFailed || execution.State == types.PredictionStateCancelled ||
			execution.State == types.PredictionStateSkipped) {
		parts = append(parts, message)
	}
	return strings.Join(parts, " | ")
}

func predictionExportLine(exam repositoryTypes.PredictionExamRow, execution types.PredictionExecution) []string {
	modality := trimmedPointerValue(execution.Modality)
	if modality == "" {
		modality = trimmedPointerValue(exam.ModalitiesInStudy)
	}
	launchedBy := "Automatic"
	if execution.RequestedByUserID != nil {
		launchedBy = *execution.RequestedByUserID
		if execution.RequestedByName != nil {
			launchedBy = *execution.RequestedByName
		}
	}
	duration := ""
	if execution.DurationSeconds != nil {
		duration = strconv.FormatFloat(*execution.DurationSeconds, 'f', 1, 64)
	}
	line := []string{
		execution.RunCreatedAt.UTC().Format(time.RFC3339),
		exam.StudyInstanceUID,
		trimmedPointerValue(exam.AccessionNumber),
		trimmedPointerValue(exam.PatientID),
		"",
		modality,
		execution.ModelName,
		trimmedPointerValue(execution.ModelVersion),
		predictionTriggerLabel(execution.Trigger),
		launchedBy,
		string(execution.State),
		duration,
		"",
		predictionReason(execution),
		execution.RunID,
		execution.ExecutionID,
	}
	for index := range line {
		line[index] = CSVSafeCell(line[index])
	}
	return line
}

// ExportPredictions writes one CSV line per model run of the exams of the
// view and filters, most recent exam first. Validation and authorization
// happen before start is called, so a refused request writes nothing; start
// receives whether the export is cut to MaximumPredictionsExportExams.
func (service *InferencePredictionsService) ExportPredictions(
	ctx context.Context,
	data types.ListPredictions,
	start func(truncated bool) io.Writer,
) (types.PredictionsExportSummary, error) {
	data.Limit, data.Offset = predictionsExportBatch, 0
	query, err := buildPredictionsQuery(data, predictionsExportBatch)
	if err != nil {
		return types.PredictionsExportSummary{}, err
	}
	examQuery := query.exams
	page, err := service.Repository.ListPredictionExams(ctx, examQuery)
	if err != nil {
		return types.PredictionsExportSummary{}, err
	}
	summary := types.PredictionsExportSummary{Truncated: page.Total > MaximumPredictionsExportExams}
	writer := csv.NewWriter(start(summary.Truncated))
	if err := writer.Write(PredictionsExportHeader); err != nil {
		return summary, err
	}

	exported := 0
	for {
		if len(page.Exams) > MaximumPredictionsExportExams-exported {
			page.Exams = page.Exams[:MaximumPredictionsExportExams-exported]
		}
		if err := service.writePredictionExportBatch(ctx, query, page.Exams, writer, &summary); err != nil {
			return summary, err
		}
		exported += len(page.Exams)
		writer.Flush()
		if err := writer.Error(); err != nil {
			return summary, err
		}
		if len(page.Exams) < examQuery.Limit || exported >= MaximumPredictionsExportExams || exported >= page.Total {
			return summary, nil
		}
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		examQuery.Offset += examQuery.Limit
		page, err = service.Repository.ListPredictionExams(ctx, examQuery)
		if err != nil {
			return summary, err
		}
	}
}

func (service *InferencePredictionsService) writePredictionExportBatch(
	ctx context.Context,
	query predictionsQuery,
	exams []repositoryTypes.PredictionExamRow,
	writer *csv.Writer,
	summary *types.PredictionsExportSummary,
) error {
	if len(exams) == 0 {
		return nil
	}
	uids := make([]string, 0, len(exams))
	byUID := make(map[string]repositoryTypes.PredictionExamRow, len(exams))
	for _, exam := range exams {
		uids = append(uids, exam.StudyInstanceUID)
		byUID[exam.StudyInstanceUID] = exam
	}
	rows, err := service.Repository.ListPredictionExecutions(ctx, repositoryTypes.ListPredictionExecutions{
		TenantID: query.exams.TenantID, StudyInstanceUIDs: uids, ModelName: query.exams.ModelName,
		LaunchedBy: query.lineScope, RequestedByUserID: query.lineUserID,
	})
	if err != nil {
		return err
	}
	names := service.userNames(ctx, query.exams.TenantID, rows, nil)
	grouped := make(map[string][]types.PredictionExecution, len(exams))
	for index := range rows {
		row := &rows[index]
		grouped[row.StudyInstanceUID] = append(grouped[row.StudyInstanceUID], buildPredictionExecution(row, query.userID, names))
	}
	// Keep the page order: most recent exam first, newest run first within it.
	for _, uid := range uids {
		for _, execution := range grouped[uid] {
			if err := writer.Write(predictionExportLine(byUID[uid], execution)); err != nil {
				return err
			}
			summary.Rows++
		}
	}
	return nil
}
