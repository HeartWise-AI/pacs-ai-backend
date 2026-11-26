package service

import (
	"context"
	"errors"
	"log"
	"strings"
	"unicode"
	"unicode/utf8"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/infrastructure/service/types"
)

// RunLabelMaxLength is the longest label, in characters, once trimmed and
// with inner whitespace collapsed (study-service applies the same rule).
const RunLabelMaxLength = 40

var errInvalidRunLabel = errors.New("invalid run label")

// NormalizeRunLabel trims a free-text label and collapses its inner
// whitespace. Labels are matched without case by study-service.
func NormalizeRunLabel(text string) (string, error) {
	label := strings.Join(strings.Fields(text), " ")
	if label == "" || utf8.RuneCountInString(label) > RunLabelMaxLength {
		return "", errInvalidRunLabel
	}
	for _, char := range label {
		if unicode.IsControl(char) {
			return "", errInvalidRunLabel
		}
	}
	return label, nil
}

// runLabelError maps a study-service label failure to an API error.
func runLabelError(tenantID, jobID string, err error) error {
	switch {
	case errors.Is(err, errStudyServiceRunLabelNotFound):
		return errors.New(apiError.MissingRecord)
	case errors.Is(err, errStudyServiceRunLabelInvalid):
		return errors.New(apiError.InvalidPayload)
	}
	log.Printf("[Run labels] study-service call failed tenant_id=%s job_id=%s err=%v", tenantID, jobID, err)
	return errors.New(apiError.InferenceResultServiceUnavailable)
}

// runLabelsOf checks that the job is one of the tenant's runs of the study
// and of a model the caller may see, and returns its labels.
func (service *InferenceQueryService) runLabelsOf(ctx context.Context, data types.GetRunLabels) (types.StudyServiceJobLabels, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	studyInstanceUID := strings.TrimSpace(data.StudyInstanceUID)
	jobID := strings.TrimSpace(data.StudyServiceJobID)
	if tenantID == "" || studyInstanceUID == "" || jobID == "" || len(studyInstanceUID) > maximumViewerStudyUIDLength {
		return types.StudyServiceJobLabels{}, errors.New(apiError.InvalidPayload)
	}
	if service.RunLabelProvider == nil {
		return types.StudyServiceJobLabels{}, errors.New(apiError.InferenceResultServiceUnavailable)
	}
	labels, found, err := service.RunLabelProvider.GetJobLabels(ctx, tenantID, jobID)
	if err != nil {
		return types.StudyServiceJobLabels{}, runLabelError(tenantID, jobID, err)
	}
	if !found || strings.TrimSpace(labels.JobID) != jobID || strings.TrimSpace(labels.StudyInstanceUID) != studyInstanceUID {
		return types.StudyServiceJobLabels{}, errors.New(apiError.MissingRecord)
	}
	if err := service.authorizeResultModel(ctx, tenantID, labels.ModelName, trimmedPointerValue(labels.ModelVersion), data.AllowModel); err != nil {
		return types.StudyServiceJobLabels{}, err
	}
	return labels, nil
}

func viewerRunLabels(labels types.StudyServiceJobLabels) types.RunLabels {
	list := labels.Labels
	if list == nil {
		list = []types.RunLabel{}
	}
	return types.RunLabels{
		StudyInstanceUID:  labels.StudyInstanceUID,
		StudyServiceJobID: labels.JobID,
		ModelName:         labels.ModelName,
		Modality:          labels.Modality,
		Labels:            list,
	}
}

// GetStudyRunLabels returns the labels of one run of a study.
func (service *InferenceQueryService) GetStudyRunLabels(ctx context.Context, data types.GetRunLabels) (types.RunLabels, error) {
	labels, err := service.runLabelsOf(ctx, data)
	if err != nil {
		return types.RunLabels{}, err
	}
	return viewerRunLabels(labels), nil
}

// AddStudyRunLabel puts a free-text label on one run of a study; added is
// false when the run already carried it (in any case).
func (service *InferenceQueryService) AddStudyRunLabel(ctx context.Context, data types.ChangeRunLabel) (types.RunLabels, bool, error) {
	label, err := NormalizeRunLabel(data.Label)
	if err != nil {
		return types.RunLabels{}, false, errors.New(apiError.InvalidPayload)
	}
	if _, err := service.runLabelsOf(ctx, data.GetRunLabels); err != nil {
		return types.RunLabels{}, false, err
	}
	tenantID, jobID := strings.TrimSpace(data.TenantID), strings.TrimSpace(data.StudyServiceJobID)
	labels, added, err := service.RunLabelProvider.AddJobLabel(ctx, tenantID, jobID, label, data.UserID)
	if err != nil {
		return types.RunLabels{}, false, runLabelError(tenantID, jobID, err)
	}
	log.Printf("[Run labels] added tenant_id=%s job_id=%s user_id=%s added=%t", tenantID, jobID, data.UserID, added)
	return viewerRunLabels(labels), added, nil
}

// RemoveStudyRunLabel takes a label off one run of a study.
func (service *InferenceQueryService) RemoveStudyRunLabel(ctx context.Context, data types.ChangeRunLabel) (types.RunLabels, error) {
	label, err := NormalizeRunLabel(data.Label)
	if err != nil {
		return types.RunLabels{}, errors.New(apiError.InvalidPayload)
	}
	if _, err := service.runLabelsOf(ctx, data.GetRunLabels); err != nil {
		return types.RunLabels{}, err
	}
	tenantID, jobID := strings.TrimSpace(data.TenantID), strings.TrimSpace(data.StudyServiceJobID)
	labels, err := service.RunLabelProvider.RemoveJobLabel(ctx, tenantID, jobID, label)
	if err != nil {
		return types.RunLabels{}, runLabelError(tenantID, jobID, err)
	}
	log.Printf("[Run labels] removed tenant_id=%s job_id=%s user_id=%s", tenantID, jobID, data.UserID)
	return viewerRunLabels(labels), nil
}

// GetRunLabelCatalog lists the labels the tenant has used, for suggestions.
func (service *InferenceQueryService) GetRunLabelCatalog(ctx context.Context, tenantID string) (types.RunLabelCatalog, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return types.RunLabelCatalog{}, errors.New(apiError.InvalidPayload)
	}
	if service.RunLabelProvider == nil {
		return types.RunLabelCatalog{}, errors.New(apiError.InferenceResultServiceUnavailable)
	}
	catalog, err := service.RunLabelProvider.GetRunLabelCatalog(ctx, tenantID)
	if err != nil {
		return types.RunLabelCatalog{}, runLabelError(tenantID, "", err)
	}
	return catalog, nil
}
