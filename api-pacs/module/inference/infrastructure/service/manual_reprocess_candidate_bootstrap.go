package service

import (
	"context"
	"errors"
	"log"
	"slices"
	"strings"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	orthancServiceTypes "api-pacs/module/orthanc/infrastructure/service/types"
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

func (service *InferenceCommandService) bootstrapManualStudyCandidates(
	ctx context.Context,
	tenantID string,
	studyInstanceUID string,
) ([]entity.InferenceIngestionCandidate, error) {
	candidates, err := service.bootstrapLocalManualStudyCandidates(ctx, tenantID, studyInstanceUID)
	if err == nil || err.Error() != apiError.MissingRecord {
		return candidates, err
	}
	return service.bootstrapRemoteManualStudyCandidates(ctx, tenantID, studyInstanceUID)
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

type remoteManualCandidateSelection struct {
	job   entity.InferenceIngestionJob
	study orthancAPITypes.QueryModalityStudyAnswersResponse
}

// bootstrapRemoteManualStudyCandidates performs an exact C-FIND against each
// configured source needed by the tenant's running ingestion jobs. Candidates
// remain DISCOVERED until the processing-run planner atomically exists; the
// existing manual flow then moves them to RETRIEVAL_QUEUED and uses its normal
// C-MOVE/retrieval reconciliation path.
func (service *InferenceCommandService) bootstrapRemoteManualStudyCandidates(
	ctx context.Context,
	tenantID string,
	studyInstanceUID string,
) ([]entity.InferenceIngestionCandidate, error) {
	if service.OrthancQueryServiceInterface == nil {
		return nil, errors.New(apiError.MissingRecord)
	}
	jobs, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobs(&tenantID)
	if err != nil {
		return nil, err
	}
	jobsBySource := make(map[string][]entity.InferenceIngestionJob)
	for _, job := range jobs {
		source := strings.TrimSpace(job.DICOMModality)
		if job.TenantID != tenantID || job.Status != entity.InferenceIngestionJobStatusRunning ||
			source == "" || strings.EqualFold(source, viewerIngestionJobSource) {
			continue
		}
		jobsBySource[source] = append(jobsBySource[source], job)
	}
	if len(jobsBySource) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	sources := make([]string, 0, len(jobsBySource))
	for source := range jobsBySource {
		sources = append(sources, source)
	}
	slices.Sort(sources)
	selectedByModel := make(map[string]remoteManualCandidateSelection)
	for _, source := range sources {
		studies, _, findErr := service.OrthancQueryServiceInterface.FindModalityStudies(ctx, orthancServiceTypes.FindModalityStudies{
			TenantID: tenantID, ModalityID: source, StudyInstanceUID: studyInstanceUID,
		})
		if findErr != nil {
			if findErr.Error() == apiError.MissingRecord {
				continue
			}
			return nil, findErr
		}
		exact := make([]orthancAPITypes.QueryModalityStudyAnswersResponse, 0, 1)
		for _, study := range studies {
			if strings.TrimSpace(study.StudyInstanceUID) == studyInstanceUID {
				exact = append(exact, study)
			}
		}
		if len(exact) == 0 {
			continue
		}
		if len(exact) != 1 {
			return nil, errors.New(apiError.MissingRecord)
		}
		study := exact[0]
		studyModalities := parseCandidateModalities(nonEmptyStringPointer(strings.TrimSpace(study.ModalitiesInStudy)))
		if len(studyModalities) == 0 {
			continue
		}
		studyModalitySet := make(map[string]struct{}, len(studyModalities))
		for _, modality := range studyModalities {
			studyModalitySet[modality] = struct{}{}
		}
		for _, job := range jobsBySource[source] {
			if !ingestionJobSupportsStudyModality(job, studyModalitySet) {
				continue
			}
			modelKey := strings.ToLower(strings.TrimSpace(job.ModelName))
			if modelKey == "" {
				continue
			}
			current, exists := selectedByModel[modelKey]
			if !exists || manualReprocessJobIsNewer(job, current.job) {
				selectedByModel[modelKey] = remoteManualCandidateSelection{job: job, study: study}
			}
		}
	}
	if len(selectedByModel) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	selections := make([]remoteManualCandidateSelection, 0, len(selectedByModel))
	for _, selection := range selectedByModel {
		selections = append(selections, selection)
	}
	slices.SortFunc(selections, func(left, right remoteManualCandidateSelection) int {
		return strings.Compare(left.job.ID, right.job.ID)
	})
	upserts := make([]repositoryTypes.UpsertIngestionCandidate, 0, len(selections))
	for _, selection := range selections {
		study := selection.study
		seriesCount, _ := parseNullableInt(study.NumberOfStudyRelatedSeries)
		instanceCount, _ := parseNullableInt(study.NumberOfStudyRelatedInstances)
		upserts = append(upserts, repositoryTypes.UpsertIngestionCandidate{
			ID: generateID(), TenantID: tenantID, IngestionJobID: selection.job.ID,
			StudyInstanceUID: studyInstanceUID,
			StudyDate:        nullableString(study.StudyDate), StudyTime: nullableString(study.StudyTime),
			ModalitiesInStudy: nonEmptyStringPointer(strings.TrimSpace(study.ModalitiesInStudy)),
			PatientID:         nullableString(study.PatientID), AccessionNumber: nullableString(study.AccessionNumber),
			SeriesCount: seriesCount, InstanceCount: instanceCount,
		})
	}
	if err := service.InferenceCommandRepositoryInterface.UpsertIngestionCandidates(ctx, upserts); err != nil {
		return nil, err
	}

	candidates, err := service.InferenceQueryRepositoryInterface.ListInferenceIngestionCandidates(repositoryTypes.ListInferenceIngestionCandidates{
		TenantID: tenantID, StudyInstanceUID: &studyInstanceUID,
	})
	if err != nil {
		return nil, err
	}
	selectedJobIDs := make(map[string]struct{}, len(selections))
	for _, selection := range selections {
		selectedJobIDs[selection.job.ID] = struct{}{}
	}
	ready := make([]entity.InferenceIngestionCandidate, 0, len(selections))
	for _, candidate := range candidates {
		if _, selected := selectedJobIDs[candidate.IngestionJobID]; !selected {
			continue
		}
		if candidate.TenantID != tenantID || strings.TrimSpace(candidate.StudyInstanceUID) != studyInstanceUID ||
			candidate.Status == entity.InferenceIngestionCandidateStatusDisappeared {
			return nil, errors.New(apiError.DatabaseError)
		}
		ready = append(ready, candidate)
	}
	if len(ready) != len(selections) {
		return nil, errors.New(apiError.DatabaseError)
	}
	log.Printf("[Manual reprocess] bootstrapped remote study candidates tenant_id=%s study_instance_uid=%s candidates=%d",
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
