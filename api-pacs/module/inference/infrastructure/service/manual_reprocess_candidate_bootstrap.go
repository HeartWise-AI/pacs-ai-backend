package service

import (
	"context"
	"errors"
	"log"
	"strings"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
)

var manualReprocessCandidateTags = []string{
	"ModalitiesInStudy",
	"StudyDate",
	"StudyTime",
	"PatientID",
	"AccessionNumber",
	"NumberOfStudyRelatedSeries",
	"NumberOfStudyRelatedInstances",
}

// bootstrapLocalManualStudyCandidates creates the model-specific candidate
// records that recent-window discovery never created for an older local study.
// Only running, non-viewer ingestion jobs whose configured modalities intersect
// the study modalities are eligible. One current job is selected per model.
func (service *InferenceCommandService) bootstrapLocalManualStudyCandidates(
	ctx context.Context,
	tenantID string,
	studyInstanceUID string,
) ([]entity.InferenceIngestionCandidate, error) {
	if service.OrthancAPIInterface == nil {
		return nil, errors.New(apiError.MissingRecord)
	}
	resources, err := service.OrthancAPIInterface.FindLocalResources(ctx, orthancAPITypes.QueryLocalResourceRequest{
		Level: "Study",
		Query: orthancAPITypes.QueryLocalResource{
			StudyInstanceUID: studyInstanceUID,
		},
		Expand:        true,
		RequestedTags: append([]string(nil), manualReprocessCandidateTags...),
	})
	if err != nil {
		if err.Error() == apiError.MissingRecord {
			return nil, errors.New(apiError.MissingRecord)
		}
		return nil, err
	}
	if len(resources) != 1 {
		return nil, errors.New(apiError.MissingRecord)
	}

	tags := resources[0].RequestedTags
	modalitiesInStudy := strings.TrimSpace(tags["ModalitiesInStudy"])
	studyModalities := parseCandidateModalities(nonEmptyStringPointer(modalitiesInStudy))
	if len(studyModalities) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}
	studyModalitySet := make(map[string]struct{}, len(studyModalities))
	for _, modality := range studyModalities {
		studyModalitySet[modality] = struct{}{}
	}

	jobs, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobs(&tenantID)
	if err != nil {
		return nil, err
	}
	selectedByModel := make(map[string]entity.InferenceIngestionJob, len(jobs))
	for _, job := range jobs {
		if job.TenantID != tenantID || job.Status != entity.InferenceIngestionJobStatusRunning ||
			strings.TrimSpace(job.DICOMModality) == viewerIngestionJobSource ||
			!ingestionJobSupportsStudyModality(job, studyModalitySet) {
			continue
		}
		modelKey := strings.ToLower(strings.TrimSpace(job.ModelName))
		if modelKey == "" {
			continue
		}
		current, exists := selectedByModel[modelKey]
		if !exists || manualReprocessJobIsNewer(job, current) {
			selectedByModel[modelKey] = job
		}
	}
	selectedJobs := make([]entity.InferenceIngestionJob, 0, len(selectedByModel))
	for _, job := range selectedByModel {
		selectedJobs = append(selectedJobs, job)
	}
	if len(selectedJobs) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	seriesCount, _ := parseNullableInt(tags["NumberOfStudyRelatedSeries"])
	instanceCount, _ := parseNullableInt(tags["NumberOfStudyRelatedInstances"])
	upserts := make([]repositoryTypes.UpsertIngestionCandidate, 0, len(selectedJobs))
	for _, job := range selectedJobs {
		upserts = append(upserts, repositoryTypes.UpsertIngestionCandidate{
			ID:                generateID(),
			TenantID:          tenantID,
			IngestionJobID:    job.ID,
			StudyInstanceUID:  studyInstanceUID,
			StudyDate:         nullableString(tags["StudyDate"]),
			StudyTime:         nullableString(tags["StudyTime"]),
			ModalitiesInStudy: nonEmptyStringPointer(modalitiesInStudy),
			PatientID:         nullableString(tags["PatientID"]),
			AccessionNumber:   nullableString(tags["AccessionNumber"]),
			SeriesCount:       seriesCount,
			InstanceCount:     instanceCount,
		})
	}
	if err := service.InferenceCommandRepositoryInterface.UpsertRetrievedIngestionCandidates(ctx, upserts); err != nil {
		return nil, err
	}

	candidates, err := service.InferenceQueryRepositoryInterface.ListInferenceIngestionCandidates(repositoryTypes.ListInferenceIngestionCandidates{
		TenantID: tenantID, StudyInstanceUID: &studyInstanceUID,
	})
	if err != nil {
		return nil, err
	}
	selectedJobIDs := make(map[string]struct{}, len(selectedJobs))
	for _, job := range selectedJobs {
		selectedJobIDs[job.ID] = struct{}{}
	}
	ready := make([]entity.InferenceIngestionCandidate, 0, len(selectedJobs))
	for _, candidate := range candidates {
		if _, selected := selectedJobIDs[candidate.IngestionJobID]; !selected {
			continue
		}
		if candidate.TenantID != tenantID || strings.TrimSpace(candidate.StudyInstanceUID) != studyInstanceUID {
			return nil, errors.New(apiError.DatabaseError)
		}
		if candidate.Status != entity.InferenceIngestionCandidateStatusRetrieved {
			return nil, errors.New(apiError.DatabaseError)
		}
		ready = append(ready, candidate)
	}
	if len(ready) != len(selectedJobs) {
		return nil, errors.New(apiError.DatabaseError)
	}
	log.Printf("[Manual reprocess] bootstrapped local study candidates tenant_id=%s study_instance_uid=%s candidates=%d",
		tenantID, studyInstanceUID, len(ready))
	return ready, nil
}

// Job activity mutates updated_at, so it cannot identify the latest job
// definition. Creation time is stable; ID provides a deterministic tie-break.
func manualReprocessJobIsNewer(candidate, current entity.InferenceIngestionJob) bool {
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	return strings.Compare(candidate.ID, current.ID) > 0
}

func ingestionJobSupportsStudyModality(job entity.InferenceIngestionJob, studyModalities map[string]struct{}) bool {
	for _, modality := range normalizeDispatchModalities([]string(job.Modalities)) {
		if _, supported := studyModalities[modality]; supported {
			return true
		}
	}
	return false
}
