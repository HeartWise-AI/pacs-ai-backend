package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/infrastructure/service/types"
)

// StoreBackStudyLookup reads the patient of a study cached in the local Orthanc.
type StoreBackStudyLookup interface {
	FindLocalResources(ctx context.Context, request orthancAPITypes.QueryLocalResourceRequest) ([]orthancAPITypes.GetLocalResourceResponse, error)
}

// StoreBackReports resolves the report a store-back may send to a PACS from a
// completed, tenant-owned processing execution. The reference is
// "<processingRunID>:<executionID>". Only a report the model renders as PDF
// can be sent; HTML reports are refused (no server-side HTML to PDF).
type StoreBackReports struct {
	Results *InferenceQueryService
	Studies StoreBackStudyLookup
}

// RegisteredStoreBackReport implements orthanc service.StoreBackReportSource.
func (reports *StoreBackReports) RegisteredStoreBackReport(ctx context.Context, tenantID, reference string) (string, string, string, string, string, []byte, []string, error) {
	fail := func(code string) (string, string, string, string, string, []byte, []string, error) {
		return "", "", "", "", "", nil, nil, errors.New(code)
	}
	if reports == nil || reports.Results == nil || reports.Studies == nil {
		return fail(apiError.MissingRecord)
	}
	runID, executionID, found := strings.Cut(strings.TrimSpace(reference), ":")
	if !found || strings.TrimSpace(runID) == "" || strings.TrimSpace(executionID) == "" {
		return fail(apiError.MissingRecord)
	}
	result, err := reports.Results.GetProcessingRunExecutionResult(ctx, types.GetProcessingRunExecutionResult{
		TenantID: tenantID, RunID: runID, ExecutionID: executionID,
	})
	if err != nil {
		return fail(apiError.MissingRecord)
	}
	modelVersion := trimmedPointerValue(result.ModelVersion)
	report, err := reports.Results.renderStoredResult(ctx, strings.TrimSpace(tenantID), result.ModelName, modelVersion, result.Result)
	if err != nil {
		return fail(apiError.MissingRecord)
	}
	pdf, err := base64.StdEncoding.DecodeString(report.HTMLBase64)
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF")) {
		return fail(apiError.ReportNotAvailable)
	}
	if report.ModelVersion != "" {
		modelVersion = report.ModelVersion
	}

	studies, err := reports.Studies.FindLocalResources(ctx, orthancAPITypes.QueryLocalResourceRequest{
		Level:         "Study",
		Query:         orthancAPITypes.QueryLocalResource{StudyInstanceUID: result.StudyInstanceUID},
		RequestedTags: []string{"PatientID", "PatientName"},
	})
	if err != nil || len(studies) != 1 {
		return fail(apiError.MissingRecord)
	}
	patientID := strings.TrimSpace(studies[0].RequestedTags["PatientID"])
	patientName := strings.TrimSpace(studies[0].RequestedTags["PatientName"])
	if patientID == "" {
		return fail(apiError.MissingRecord)
	}
	return patientID, patientName, result.StudyInstanceUID, result.ModelName, modelVersion, pdf, []string{executionID}, nil
}
