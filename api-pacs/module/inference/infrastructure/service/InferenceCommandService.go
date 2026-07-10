package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	dicomUtils "api-pacs/internal/dicom"
	apiError "api-pacs/internal/errors"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	inferenceApplication "api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
	orthancApplication "api-pacs/module/orthanc/application"
	orthancServiceTypes "api-pacs/module/orthanc/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	userApplication "api-pacs/module/user/application"
)

// InferenceCommandService handles the Inference command service logic
type InferenceCommandService struct {
	repository.InferenceCommandRepositoryInterface
	repository.InferenceQueryRepositoryInterface
	tenantApplication.TenantQueryServiceInterface
	userApplication.UserQueryServiceInterface
	orthancApplication.OrthancCommandServiceInterface
	orthancApplication.OrthancQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	dockerTypes.DockerSDKInterface
	orthancAPITypes.OrthancAPIInterface
	dockerInferenceTypes.DockerInferenceAPIInterface
	inferenceApplication.ProcessingDispatcherInterface
	StudyServiceDispatchSemaphore chan struct{}
}

const inferenceIngestionRetrievalTimeout = 3 * time.Minute
const inferenceIngestionRetrievalPollInterval = 2 * time.Second
const studyServiceDispatchAttemptTimeout = 2 * time.Second

const (
	defaultRecentWindowMinutes        uint = 240
	defaultStabilityMinutes           uint = 10
	defaultMissingPollsThreshold      uint = 3
	defaultReconciliationStaleMinutes uint = 15
)

var studyServiceDispatchRetrySchedule = []time.Duration{
	0,
	2 * time.Second,
	8 * time.Second,
}

type candidateRetrievalOutcome string

const (
	candidateRetrievalOutcomeLocal   candidateRetrievalOutcome = "local"
	candidateRetrievalOutcomeSuccess candidateRetrievalOutcome = "success"
	candidateRetrievalOutcomeFailure candidateRetrievalOutcome = "failure"
	candidateRetrievalOutcomeTimeout candidateRetrievalOutcome = "timeout"
)

type candidateRetrievalResult struct {
	Outcome                   candidateRetrievalOutcome
	OrthancJobIDs             []string
	LastRetrievalState        *string
	LastRetrievalError        *string
	LastRetrievalErrorDetails *string
}

type normalizedIngestionJobConfig struct {
	StabilityMinutes      uint
	RecentWindowMinutes   uint
	MissingPollsThreshold uint
	StudyTimeStart        *string
	StudyTimeEnd          *string
}

// BuildStudyServiceDispatchRequest resolves the study-service ingest payload for one job/candidate pair.
func (service *InferenceCommandService) BuildStudyServiceDispatchRequest(ctx context.Context, data types.BuildStudyServiceDispatchRequestInput) (types.DispatchStudyRequest, error) {
	return service.ProcessingDispatcherInterface.BuildDispatchStudyRequest(ctx, data)
}

// DispatchStudy sends a POST /ingest/study request to study-service.
func (service *InferenceCommandService) DispatchStudy(ctx context.Context, data types.DispatchStudyRequest) (types.DispatchStudyResponse, error) {
	return service.ProcessingDispatcherInterface.DispatchStudy(ctx, data)
}

// HandleStudyServiceProcessingCallback persists a study-service processing callback.
func (service *InferenceCommandService) HandleStudyServiceProcessingCallback(ctx context.Context, data types.HandleStudyServiceProcessingCallback) (types.HandleStudyServiceProcessingCallbackResult, error) {
	candidate, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionCandidateByID(data.CandidateID)
	if err != nil {
		return types.HandleStudyServiceProcessingCallbackResult{}, err
	}

	if strings.TrimSpace(candidate.StudyInstanceUID) != strings.TrimSpace(data.StudyInstanceUID) {
		return types.HandleStudyServiceProcessingCallbackResult{}, errors.New(apiError.InvalidPayload)
	}

	status, ok := parseInferenceIngestionProcessingJobStatus(data.Status)
	if !ok {
		return types.HandleStudyServiceProcessingCallbackResult{}, errors.New(apiError.InvalidPayload)
	}

	modelName := strings.TrimSpace(data.ModelName)
	modelVersion := nonEmptyStringPointer(strings.TrimSpace(data.ModelVersion))
	modality := nonEmptyStringPointer(strings.TrimSpace(data.Modality))
	studyServiceJobID := nonEmptyStringPointer(strings.TrimSpace(data.StudyServiceJobID))
	errorMessage := trimmedPointer(data.ErrorMessage)

	existing, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionProcessingJobByCandidateModel(candidate.ID, modelName)
	if err != nil {
		if err.Error() != apiError.MissingRecord {
			return types.HandleStudyServiceProcessingCallbackResult{}, err
		}

		err = service.InferenceCommandRepositoryInterface.InsertInferenceIngestionProcessingJob(repositoryTypes.AddInferenceIngestionProcessingJob{
			ID:                generateID(),
			CandidateID:       candidate.ID,
			TenantID:          candidate.TenantID,
			ModelName:         modelName,
			ModelVersion:      modelVersion,
			Modality:          modality,
			Status:            status,
			StudyServiceJobID: studyServiceJobID,
			ErrorMessage:      errorMessage,
			StartedAt:         data.StartedAt,
			CompletedAt:       data.CompletedAt,
		})
		if err != nil {
			return types.HandleStudyServiceProcessingCallbackResult{}, err
		}

		return types.HandleStudyServiceProcessingCallbackResult{Outcome: "applied"}, nil
	}

	if !isAllowedInferenceIngestionProcessingTransition(existing.Status, status) {
		log.Printf("[Ingestion callback] ignoring out-of-order callback candidate_id=%s model_name=%s request_id=%s current_status=%s incoming_status=%s",
			candidate.ID,
			modelName,
			strings.TrimSpace(data.RequestID),
			existing.Status,
			status,
		)
		return types.HandleStudyServiceProcessingCallbackResult{Outcome: "ignored"}, nil
	}

	if existing.Status == status {
		log.Printf("[Ingestion callback] ignoring replayed callback candidate_id=%s model_name=%s request_id=%s status=%s",
			candidate.ID,
			modelName,
			strings.TrimSpace(data.RequestID),
			status,
		)
		return types.HandleStudyServiceProcessingCallbackResult{Outcome: "replayed"}, nil
	}

	err = service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionProcessingJob(repositoryTypes.UpdateInferenceIngestionProcessingJob{
		ID:                existing.ID,
		Status:            status,
		ModelVersion:      modelVersion,
		Modality:          modality,
		StudyServiceJobID: studyServiceJobID,
		ErrorMessage:      errorMessage,
		StartedAt:         data.StartedAt,
		CompletedAt:       data.CompletedAt,
	})
	if err != nil {
		return types.HandleStudyServiceProcessingCallbackResult{}, err
	}

	return types.HandleStudyServiceProcessingCallbackResult{Outcome: "applied"}, nil
}

// AddInferenceModel adds an inference model
func (service *InferenceCommandService) AddInferenceModel(ctx context.Context, data types.AddInferenceModel) error {
	// pull docker image
	err := service.DockerSDKInterface.PullImage(ctx, data.DockerImage)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// create container
	containerID, err := service.DockerSDKInterface.CreateContainer(ctx, dockerTypes.CreateContainer{
		Name:  data.Name,
		Image: data.DockerImage,
		Envs:  data.Envs,
	})
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// start container
	err = service.StartInferenceModelContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// TODO: check and compare output mode if supported

	// generate ID
	ID := generateID()

	err = service.InferenceCommandRepositoryInterface.InsertInferenceModel(ctx, repositoryTypes.AddInferenceModel{
		ID:                  ID,
		TenantID:            data.TenantID,
		ContainerID:         containerID,
		Name:                data.Name,
		DockerImage:         data.DockerImage,
		Envs:                data.Envs,
		DisallowedDICOMTags: []string{}, // initialize empty list
		OutputMode:          data.OutputMode,
	})
	if err != nil {
		return err
	}

	return nil
}

// AddOnboardingModelQuestionnaireAnswers adds an onboarding model questionnaire answers
func (service *InferenceCommandService) AddOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.AddOnboardingModelQuestionnaireAnswer) error {
	// add onboarding model questionnaire answer
	for _, answer := range data.OnboardingModelQuestionnaireAnswers {
		err := service.InferenceCommandRepositoryInterface.InsertOnboardingModelQuestionnaireAnswer(ctx, repositoryTypes.AddOnboardingModelQuestionnaireAnswer{
			ID:                     generateID(),
			TenantID:               data.TenantID,
			UserID:                 data.UserID,
			ModelID:                data.ModelID,
			QuestionnaireID:        answer.QuestionnaireID,
			QuestionnaireQuestion:  answer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   answer.QuestionnaireAnswers,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// CreateInferenceIngestionJob creates a new inference ingestion job
func (service *InferenceCommandService) CreateInferenceIngestionJob(ctx context.Context, data types.CreateInferenceIngestionJob) error {
	jobConfig, err := normalizeIngestionJobConfig(data.StabilityMinutes, data.RecentWindowMinutes, data.MissingPollsThreshold, data.StudyTimeStart, data.StudyTimeEnd)
	if err != nil {
		return err
	}

	err = service.InferenceCommandRepositoryInterface.InsertInferenceIngestionJob(repositoryTypes.CreateInferenceIngestionJob{
		ID:                     generateID(),
		TenantID:               data.TenantID,
		DICOMModality:          data.DICOMModality,
		ContainerID:            data.ContainerID,
		ModelID:                data.ModelID,
		ModelName:              data.ModelName,
		ModelVersion:           data.ModelVersion,
		Modalities:             data.Modalities,
		RecentWindowMinutes:    jobConfig.RecentWindowMinutes,
		StabilityMinutes:       jobConfig.StabilityMinutes,
		MissingPollsThreshold:  jobConfig.MissingPollsThreshold,
		StudyTimeStart:         jobConfig.StudyTimeStart,
		StudyTimeEnd:           jobConfig.StudyTimeEnd,
		ScheduleStartTimestamp: time.Unix(int64(data.ScheduleStartTimestamp), 0),
		ScheduleEndTimestamp:   time.Unix(int64(data.ScheduleEndTimestamp), 0),
		Status:                 entity.InferenceIngestionJobStatusRunning, // default: RUNNING
	})
	if err != nil {
		return err
	}

	return nil
}

// ExecuteInferenceIngestionRunner execute inference ingestion runner
func (service *InferenceCommandService) ExecuteInferenceIngestionRunner(ctx context.Context) error {
	/// step 1: get all inference ingestion jobs
	jobs, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobs(nil) // all tenant
	if err != nil && err.Error() != apiError.MissingRecord {
		return err
	}

	log.Printf("[Ingestion Service] found %d inference ingestion jobs", len(jobs))
	for _, job := range jobs {
		log.Printf("[Ingestion Service] job_id=%s tenant_id=%s dicom_modality=%s container_id=%s model_id=%s model_name=%s model_version=%s modalities=%v stability_minutes=%d recent_window_minutes=%d missing_polls_threshold=%d study_time_start=%s study_time_end=%s schedule_start_timestamp=%s schedule_end_timestamp=%s status=%s last_executed_at=%s created_at=%s updated_at=%s",
			job.ID,
			job.TenantID,
			job.DICOMModality,
			job.ContainerID,
			job.ModelID,
			job.ModelName,
			job.ModelVersion,
			[]string(job.Modalities),
			job.StabilityMinutes,
			job.RecentWindowMinutes,
			job.MissingPollsThreshold,
			nullableStringLogValue(job.StudyTimeStart),
			nullableStringLogValue(job.StudyTimeEnd),
			formatOptionalEasternTime(job.ScheduleStartTimestamp),
			formatOptionalEasternTime(job.ScheduleEndTimestamp),
			job.Status,
			nullableTimeLogValue(job.LastExecutedAt),
			formatEasternTime(job.CreatedAt),
			formatEasternTime(job.UpdatedAt),
		)
	}

	/// step 2: process each ingestion job sequentially
	runnerNow := time.Now()
	cFindCache := map[string][]orthancAPITypes.QueryModalityStudyAnswersResponse{}
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := service.executeInferenceIngestionJob(ctx, job, runnerNow, cFindCache); err != nil {
			return err
		}
	}

	return nil
}

func (service *InferenceCommandService) executeInferenceIngestionJob(ctx context.Context, job entity.InferenceIngestionJob, jobNow time.Time, cFindCache map[string][]orthancAPITypes.QueryModalityStudyAnswersResponse) error {
	recentWindowMinutes := resolvedRecentWindowMinutes(job.RecentWindowMinutes)
	stabilityMinutes := resolvedStabilityMinutes(job.StabilityMinutes)
	missingPollsThreshold := int(resolvedMissingPollsThreshold(job.MissingPollsThreshold))
	log.Printf("[Ingestion service] evaluating job_id=%s now=%s resolved_stability_minutes=%d resolved_recent_window_minutes=%d resolved_missing_polls_threshold=%d",
		job.ID,
		formatEasternTime(jobNow),
		stabilityMinutes,
		recentWindowMinutes,
		missingPollsThreshold,
	)
	queryWindows, err := buildIngestionQueryWindows(jobNow, recentWindowMinutes, job.StudyTimeStart, job.StudyTimeEnd)
	log.Printf("[Ingestion service] built query windows job_id=%s query_windows=%v", job.ID, queryWindows)
	if err != nil {
		log.Printf("[Ingestion service] invalid job query window config job_id=%s err=%v", job.ID, err)
		return nil // skip
	}

	// if not running, skip
	if job.Status != entity.InferenceIngestionJobStatusRunning {
		log.Printf("[Ingestion service] skipping job_id=%s reason=job_not_running status=%s", job.ID, job.Status)
		return nil // skip
	}

	// if schedule start is set and not reached yet, skip
	if !isDisabledScheduleTimestamp(job.ScheduleStartTimestamp) && jobNow.Before(job.ScheduleStartTimestamp) {
		log.Printf("[Ingestion service] skipping job_id=%s reason=schedule_start_not_reached schedule_start_timestamp=%s now=%s",
			job.ID,
			formatEasternTime(job.ScheduleStartTimestamp),
			formatEasternTime(jobNow),
		)
		return nil // skip
	}

	// if schedule end is set and already passed, skip
	if !isDisabledScheduleTimestamp(job.ScheduleEndTimestamp) && jobNow.After(job.ScheduleEndTimestamp) {
		log.Printf("[Ingestion service] skipping job_id=%s reason=schedule_end_passed schedule_end_timestamp=%s now=%s",
			job.ID,
			formatEasternTime(job.ScheduleEndTimestamp),
			formatEasternTime(jobNow),
		)
		return nil // skip
	}

	existingCandidates, err := service.InferenceQueryRepositoryInterface.ListCandidatesByJob(job.ID)
	if err != nil && err.Error() != apiError.MissingRecord {
		log.Println("[Ingestion service] cannot list ingestion candidates:", err)
		return nil // skip
	}
	log.Printf("[Ingestion service] loaded existing candidates job_id=%s count=%d", job.ID, len(existingCandidates))

	existingCandidateMap := map[string]entity.InferenceIngestionCandidate{}
	for _, candidate := range existingCandidates {
		existingCandidateMap[candidate.StudyInstanceUID] = candidate
	}

	/// step 3: get studies by dicom modality (with filter) - Can have two query windows for midnight boundary
	studies, err := service.findIngestionStudiesWithCache(ctx, job, queryWindows, recentWindowMinutes, stabilityMinutes, missingPollsThreshold, cFindCache)
	if err != nil {
		log.Println("[Ingestion service] cannot pull studies:", err)
		return nil // skip
	}
	log.Printf("[Ingestion service] c-find unique studies job_id=%s count=%d", job.ID, len(studies))

	windowStart := jobNow.Add(-time.Duration(recentWindowMinutes) * time.Minute)
	filteredStudies, skippedStudies := filterStudiesByRecentWindow(studies, windowStart, jobNow)
	if skippedStudies > 0 {
		log.Printf("[Ingestion service] excluded studies outside recent window job_id=%s skipped=%d window_start=%s window_end=%s",
			job.ID,
			skippedStudies,
			formatEasternTime(windowStart),
			formatEasternTime(jobNow),
		)
	}
	studies = filteredStudies
	log.Printf("[Ingestion service] studies after local recent-window filter job_id=%s count=%d", job.ID, len(studies))

	seenStudyUIDs := map[string]struct{}{}
	for _, study := range studies {
		select {
		case <-ctx.Done():
			return nil // skip
		default:
		}

		seenStudyUIDs[study.StudyInstanceUID] = struct{}{}

		studyDate := nullableString(study.StudyDate)
		studyTime := nullableString(study.StudyTime)
		modalitiesInStudy := nullableString(study.ModalitiesInStudy)
		patientID := nullableString(study.PatientID)
		accessionNumber := nullableString(study.AccessionNumber)

		seriesCount, parseSeriesErr := parseNullableInt(study.NumberOfStudyRelatedSeries)
		if parseSeriesErr != nil {
			log.Printf("[Ingestion service] cannot parse series count for study %s: %v", study.StudyInstanceUID, parseSeriesErr)
		}

		instanceCount, parseInstanceErr := parseNullableInt(study.NumberOfStudyRelatedInstances)
		if parseInstanceErr != nil {
			log.Printf("[Ingestion service] cannot parse instance count for study %s: %v", study.StudyInstanceUID, parseInstanceErr)
		}

		priorCandidate, hasPriorCandidate := existingCandidateMap[study.StudyInstanceUID]
		// log.Printf("[Ingestion service] processing study job_id=%s study_instance_uid=%s study_date=%s study_time=%s modalities_in_study=%s series_count=%s instance_count=%s has_prior_candidate=%t prior_status=%s",
		// 	job.ID,
		// 	study.StudyInstanceUID,
		// 	nullableStringLogValue(studyDate),
		// 	nullableStringLogValue(studyTime),
		// 	nullableStringLogValue(modalitiesInStudy),
		// 	nullableIntLogValue(seriesCount),
		// 	nullableIntLogValue(instanceCount),
		// 	hasPriorCandidate,
		// 	candidateStatusLogValue(priorCandidate.Status, hasPriorCandidate),
		// )

		err = service.InferenceCommandRepositoryInterface.UpsertIngestionCandidate(repositoryTypes.UpsertIngestionCandidate{
			ID:                generateID(),
			TenantID:          job.TenantID,
			IngestionJobID:    job.ID,
			StudyInstanceUID:  study.StudyInstanceUID,
			StudyDate:         studyDate,
			StudyTime:         studyTime,
			ModalitiesInStudy: modalitiesInStudy,
			PatientID:         patientID,
			AccessionNumber:   accessionNumber,
			SeriesCount:       seriesCount,
			InstanceCount:     instanceCount,
		})
		if err != nil {
			log.Println("[Ingestion service] cannot upsert ingestion candidate:", err)
			continue
		}

		if hasPriorCandidate &&
			candidateCountsChanged(priorCandidate.SeriesCount, seriesCount, priorCandidate.InstanceCount, instanceCount) &&
			shouldMarkCandidateGrowing(priorCandidate.Status) {
			log.Printf("[Ingestion service] candidate growing job_id=%s candidate_id=%s study_instance_uid=%s previous_series_count=%s new_series_count=%s previous_instance_count=%s new_instance_count=%s previous_status=%s",
				job.ID,
				priorCandidate.ID,
				study.StudyInstanceUID,
				nullableIntLogValue(priorCandidate.SeriesCount),
				nullableIntLogValue(seriesCount),
				nullableIntLogValue(priorCandidate.InstanceCount),
				nullableIntLogValue(instanceCount),
				priorCandidate.Status,
			)
			err = service.InferenceCommandRepositoryInterface.UpdateCandidateStatus(priorCandidate.ID, entity.InferenceIngestionCandidateStatusGrowing)
			if err != nil {
				log.Println("[Ingestion service] cannot update ingestion candidate status to growing:", err)
			}
		}
	}

	// Find candidates that are missing from the current poll
	for _, candidate := range existingCandidates {
		if _, seen := seenStudyUIDs[candidate.StudyInstanceUID]; seen {
			continue
		}

		if !shouldTrackCandidateMissing(candidate.Status) {
			continue
		}

		err = service.InferenceCommandRepositoryInterface.IncrementCandidateMissingPolls(candidate.ID)
		if err != nil {
			log.Println("[Ingestion service] cannot increment ingestion candidate missing polls:", err)
			continue
		}
		log.Printf("[Ingestion service] candidate missing from current poll job_id=%s candidate_id=%s study_instance_uid=%s new_missing_polls=%d threshold=%d current_status=%s",
			job.ID,
			candidate.ID,
			candidate.StudyInstanceUID,
			candidate.MissingPolls+1,
			missingPollsThreshold,
			candidate.Status,
		)

		if candidate.MissingPolls+1 >= missingPollsThreshold {
			log.Printf("[Ingestion service] candidate disappeared job_id=%s candidate_id=%s study_instance_uid=%s missing_polls=%d threshold=%d",
				job.ID,
				candidate.ID,
				candidate.StudyInstanceUID,
				candidate.MissingPolls+1,
				missingPollsThreshold,
			)
			err = service.InferenceCommandRepositoryInterface.MarkCandidateDisappeared(candidate.ID)
			if err != nil {
				log.Println("[Ingestion service] cannot mark ingestion candidate disappeared:", err)
			}
		}
	}

	// Find candidates that are stable
	refreshedCandidates, err := service.InferenceQueryRepositoryInterface.ListCandidatesByJob(job.ID)
	if err != nil && err.Error() != apiError.MissingRecord {
		log.Println("[Ingestion service] cannot refresh ingestion candidates:", err)
		return nil // skip
	}

	stableBefore := jobNow.Add(-time.Duration(stabilityMinutes) * time.Minute)
	for _, candidate := range refreshedCandidates {
		if !shouldMarkCandidateStable(candidate.Status) {
			continue
		}

		if candidate.MissingPolls > 0 {
			continue
		}

		if candidate.LastChangedAt.After(stableBefore) {
			continue
		}

		log.Printf("[Ingestion service] candidate stable job_id=%s candidate_id=%s study_instance_uid=%s last_changed_at=%s stable_before=%s series_count=%s instance_count=%s previous_status=%s",
			job.ID,
			candidate.ID,
			candidate.StudyInstanceUID,
			formatEasternTime(candidate.LastChangedAt),
			formatEasternTime(stableBefore),
			nullableIntLogValue(candidate.SeriesCount),
			nullableIntLogValue(candidate.InstanceCount),
			candidate.Status,
		)
		err = service.InferenceCommandRepositoryInterface.UpdateCandidateStatus(candidate.ID, entity.InferenceIngestionCandidateStatusStable)
		if err != nil {
			log.Println("[Ingestion service] cannot update ingestion candidate status to stable:", err)
		}
	}

	readyCandidates, err := service.InferenceQueryRepositoryInterface.ListCandidatesReadyForRetrieval(job.ID, stableBefore)
	if err != nil && err.Error() != apiError.MissingRecord {
		log.Println("[Ingestion service] cannot list stable candidates ready for retrieval:", err)
		return nil // skip
	}
	log.Printf("[Ingestion service] ready candidates for retrieval job_id=%s count=%d stable_before=%s",
		job.ID,
		len(readyCandidates),
		formatEasternTime(stableBefore),
	)

	for _, candidate := range readyCandidates {
		isLocal, err := service.isStudyPresentLocally(ctx, candidate.StudyInstanceUID)
		if err != nil {
			log.Println("[Ingestion service] cannot determine if stable ingestion candidate is already local:", err)
			continue
		}

		if isLocal {
			err = service.persistCandidateRetrievalResult(job, candidate, candidateRetrievalResult{
				Outcome:            candidateRetrievalOutcomeLocal,
				LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeLocal)),
			})
			if err != nil {
				log.Println("[Ingestion service] cannot persist already-local ingestion candidate retrieval result:", err)
			}
			continue
		}

		err = service.InferenceCommandRepositoryInterface.MarkCandidateRetrievalQueued(candidate.ID)
		if err != nil {
			log.Println("[Ingestion service] cannot mark ingestion candidate retrieval queued:", err)
			continue
		}
		log.Printf("[Ingestion service] queued candidate retrieval job_id=%s candidate_id=%s study_instance_uid=%s",
			job.ID,
			candidate.ID,
			candidate.StudyInstanceUID,
		)
	}

	/// step 4: update job last execution time
	_ = service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJobLastExecutedAt(job.ID)
	log.Printf("[Ingestion service] finished job_id=%s seen_studies=%d refreshed_candidates=%d ready_candidates=%d",
		job.ID,
		len(seenStudyUIDs),
		len(refreshedCandidates),
		len(readyCandidates),
	)

	return nil
}

// ExecuteInferenceIngestionRetrievalWorker retrieves studies that were queued by the ingestion runner.
func (service *InferenceCommandService) ExecuteInferenceIngestionRetrievalWorker(ctx context.Context) error {
	candidates, err := service.InferenceQueryRepositoryInterface.ListCandidatesQueuedForRetrieval()
	if err != nil {
		return err
	}

	log.Printf("[Ingestion retrieval worker] queued candidates count=%d", len(candidates))
	for _, candidate := range candidates {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		job, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobByID(candidate.IngestionJobID)
		if err != nil {
			log.Printf("[Ingestion retrieval worker] cannot load ingestion job candidate_id=%s ingestion_job_id=%s err=%v",
				candidate.ID,
				candidate.IngestionJobID,
				err,
			)
			continue
		}

		if job.Status != entity.InferenceIngestionJobStatusRunning {
			log.Printf("[Ingestion retrieval worker] skipping queued candidate because ingestion job is not running candidate_id=%s ingestion_job_id=%s study_instance_uid=%s status=%s",
				candidate.ID,
				candidate.IngestionJobID,
				candidate.StudyInstanceUID,
				job.Status,
			)
			continue
		}

		err = service.retrieveQueuedIngestionCandidate(ctx, job, candidate)
		if err != nil {
			log.Printf("[Ingestion retrieval worker] cannot process queued candidate candidate_id=%s study_instance_uid=%s err=%v",
				candidate.ID,
				candidate.StudyInstanceUID,
				err,
			)
		}
	}

	return nil
}

// ExecuteInferenceIngestionReconciliationWorker reconciles stale processing candidates against study-service job truth.
func (service *InferenceCommandService) ExecuteInferenceIngestionReconciliationWorker(ctx context.Context) error {
	staleMinutes := configuredProcessingReconciliationStaleMinutes()
	staleBefore := time.Now().Add(-time.Duration(staleMinutes) * time.Minute)

	candidates, err := service.InferenceQueryRepositoryInterface.ListStaleProcessingCandidates(staleBefore)
	if err != nil {
		return err
	}

	log.Printf("[Ingestion reconciliation worker] stale candidates count=%d stale_before=%s stale_minutes=%d",
		len(candidates),
		formatEasternTime(staleBefore),
		staleMinutes,
	)

	for _, candidate := range candidates {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := service.reconcileStaleProcessingCandidate(ctx, candidate); err != nil {
			log.Printf("[Ingestion reconciliation worker] cannot reconcile candidate_id=%s tenant_id=%s study_instance_uid=%s err=%v",
				candidate.ID,
				candidate.TenantID,
				candidate.StudyInstanceUID,
				err,
			)
		}
	}

	return nil
}

func (service *InferenceCommandService) reconcileStaleProcessingCandidate(ctx context.Context, candidate entity.InferenceIngestionCandidate) error {
	jobs, err := service.ProcessingDispatcherInterface.GetJobsByCandidate(ctx, candidate.TenantID, candidate.ID)
	if err != nil {
		return err
	}

	if len(jobs) == 0 {
		log.Printf("[Ingestion reconciliation worker] no study-service jobs returned candidate_id=%s tenant_id=%s study_instance_uid=%s",
			candidate.ID,
			candidate.TenantID,
			candidate.StudyInstanceUID,
		)
		return nil
	}

	for _, job := range jobs {
		if job.CandidateID != nil && strings.TrimSpace(*job.CandidateID) != "" && strings.TrimSpace(*job.CandidateID) != strings.TrimSpace(candidate.ID) {
			continue
		}

		result, err := service.HandleStudyServiceProcessingCallback(ctx, types.HandleStudyServiceProcessingCallback{
			CandidateID:       candidate.ID,
			RequestID:         fmt.Sprintf("reconcile:%s:%s", strings.TrimSpace(candidate.ID), strings.TrimSpace(job.JobID)),
			StudyInstanceUID:  strings.TrimSpace(job.StudyInstanceUID),
			ModelName:         strings.TrimSpace(job.ModelName),
			ModelVersion:      trimmedPointerValue(job.ModelVersion),
			Modality:          strings.TrimSpace(job.Modality),
			Status:            strings.TrimSpace(job.Status),
			ErrorMessage:      job.ErrorMessage,
			StudyServiceJobID: strings.TrimSpace(job.JobID),
			StartedAt:         job.StartedAt,
			CompletedAt:       job.CompletedAt,
		})
		if err != nil {
			return err
		}

		log.Printf("[Ingestion reconciliation worker] reconciled candidate_id=%s tenant_id=%s study_service_job_id=%s model_name=%s outcome=%s status=%s",
			candidate.ID,
			candidate.TenantID,
			job.JobID,
			job.ModelName,
			result.Outcome,
			job.Status,
		)
	}

	return nil
}

func (service *InferenceCommandService) findIngestionStudiesWithCache(
	ctx context.Context,
	job entity.InferenceIngestionJob,
	queryWindows []ingestionQueryWindow,
	recentWindowMinutes uint,
	stabilityMinutes uint,
	missingPollsThreshold int,
	cFindCache map[string][]orthancAPITypes.QueryModalityStudyAnswersResponse,
) ([]orthancAPITypes.QueryModalityStudyAnswersResponse, error) {
	studies := make([]orthancAPITypes.QueryModalityStudyAnswersResponse, 0)
	seenStudyWindowUIDs := map[string]struct{}{}
	modalitiesInStudy := ingestionModalitiesFilter([]string(job.Modalities))

	for index, queryWindow := range queryWindows {
		cacheKey := ingestionCFindCacheKey(job.TenantID, job.DICOMModality, modalitiesInStudy, queryWindow)
		queryStudies, ok := cFindCache[cacheKey]
		if ok {
			log.Printf("[Ingestion service] c-find cache hit job_id=%s query_index=%d query_total=%d modality_id=%s modalities_in_study=%s study_date=%s study_time=%s count=%d",
				job.ID,
				index+1,
				len(queryWindows),
				job.DICOMModality,
				modalitiesInStudy,
				queryWindow.StudyDate,
				queryWindow.StudyTime,
				len(queryStudies),
			)
		} else {
			log.Printf("[Ingestion service] querying studies job_id=%s query_index=%d query_total=%d modality_id=%s modalities_in_study=%s study_date=%s study_time=%s recent_window_minutes=%d stability_minutes=%d missing_polls_threshold=%d",
				job.ID,
				index+1,
				len(queryWindows),
				job.DICOMModality,
				modalitiesInStudy,
				queryWindow.StudyDate,
				queryWindow.StudyTime,
				recentWindowMinutes,
				stabilityMinutes,
				missingPollsThreshold,
			)
			var err error
			queryStudies, _, err = service.OrthancQueryServiceInterface.FindModalityStudies(ctx, orthancServiceTypes.FindModalityStudies{
				TenantID:                      job.TenantID,
				ModalityID:                    job.DICOMModality,
				AccessionNumber:               "",
				InstitutionName:               "",
				ModalitiesInStudy:             modalitiesInStudy,
				NumberOfStudyRelatedSeries:    "",
				NumberOfStudyRelatedInstances: "",
				PatientBirthDate:              "",
				PatientID:                     "",
				PatientName:                   "",
				PatientSex:                    "",
				ReferringPhysicianName:        "",
				RequestingPhysician:           "",
				StudyDate:                     queryWindow.StudyDate,
				StudyDescription:              "",
				StudyID:                       "",
				StudyInstanceUID:              "",
				StudyTime:                     queryWindow.StudyTime,
				UserID:                        nil,
			})
			if err != nil {
				return nil, err
			}
			cFindCache[cacheKey] = queryStudies
			log.Printf("[Ingestion service] c-find returned studies job_id=%s query_index=%d count=%d",
				job.ID,
				index+1,
				len(queryStudies),
			)
		}

		for _, study := range queryStudies {
			if _, seen := seenStudyWindowUIDs[study.StudyInstanceUID]; seen {
				continue
			}
			seenStudyWindowUIDs[study.StudyInstanceUID] = struct{}{}
			studies = append(studies, study)
		}
	}

	return studies, nil
}

type ingestionQueryWindow struct {
	StudyDate string
	StudyTime string
}

func (service *InferenceCommandService) retrieveQueuedIngestionCandidate(ctx context.Context, job entity.InferenceIngestionJob, candidate entity.InferenceIngestionCandidate) error {
	log.Printf("[Ingestion retrieval worker] retrieving candidate job_id=%s candidate_id=%s study_instance_uid=%s",
		job.ID,
		candidate.ID,
		candidate.StudyInstanceUID,
	)

	retrievalResult, err := service.retrieveStableCandidate(ctx, job, candidate)
	if err != nil {
		log.Println("[Ingestion retrieval worker] cannot retrieve queued ingestion candidate:", err)
		markErr := service.InferenceCommandRepositoryInterface.MarkCandidateFailedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
			ID:                 candidate.ID,
			LastRetrievalState: stringPointer("error"),
			LastRetrievalError: stringPointer(err.Error()),
		})
		if markErr != nil {
			log.Println("[Ingestion retrieval worker] cannot persist retrieval error for ingestion candidate:", markErr)
			return markErr
		}
		return nil
	}

	return service.persistCandidateRetrievalResult(job, candidate, retrievalResult)
}

func (service *InferenceCommandService) persistCandidateRetrievalResult(job entity.InferenceIngestionJob, candidate entity.InferenceIngestionCandidate, retrievalResult candidateRetrievalResult) error {
	retrievalState := stringPointer(string(retrievalResult.Outcome))
	switch retrievalResult.Outcome {
	case candidateRetrievalOutcomeLocal, candidateRetrievalOutcomeSuccess:
		err := service.InferenceCommandRepositoryInterface.MarkCandidateRetrievedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
			ID:                        candidate.ID,
			OrthancJobIDs:             retrievalResult.OrthancJobIDs,
			LastRetrievalState:        coalesceStringPointer(retrievalResult.LastRetrievalState, retrievalState),
			LastRetrievalError:        retrievalResult.LastRetrievalError,
			LastRetrievalErrorDetails: retrievalResult.LastRetrievalErrorDetails,
		})
		if err != nil {
			log.Println("[Ingestion retrieval worker] cannot mark ingestion candidate retrieved:", err)
			return err
		}

		service.scheduleStudyServiceDispatch(job, candidate)
		return nil
	case candidateRetrievalOutcomeFailure:
		err := service.InferenceCommandRepositoryInterface.MarkCandidateFailedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
			ID:                        candidate.ID,
			OrthancJobIDs:             retrievalResult.OrthancJobIDs,
			LastRetrievalState:        coalesceStringPointer(retrievalResult.LastRetrievalState, retrievalState),
			LastRetrievalError:        coalesceStringPointer(retrievalResult.LastRetrievalError, stringPointer("Orthanc retrieval failed")),
			LastRetrievalErrorDetails: retrievalResult.LastRetrievalErrorDetails,
		})
		if err != nil {
			log.Println("[Ingestion retrieval worker] cannot mark ingestion candidate failed:", err)
		}
		return err
	case candidateRetrievalOutcomeTimeout:
		err := service.InferenceCommandRepositoryInterface.MarkCandidateFailedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
			ID:                        candidate.ID,
			OrthancJobIDs:             retrievalResult.OrthancJobIDs,
			LastRetrievalState:        coalesceStringPointer(retrievalResult.LastRetrievalState, retrievalState),
			LastRetrievalError:        coalesceStringPointer(retrievalResult.LastRetrievalError, stringPointer("Orthanc retrieval timed out")),
			LastRetrievalErrorDetails: retrievalResult.LastRetrievalErrorDetails,
		})
		if err != nil {
			log.Println("[Ingestion retrieval worker] cannot persist retrieval timeout for ingestion candidate:", err)
		}
		return err
	}

	return nil
}

func (service *InferenceCommandService) scheduleStudyServiceDispatch(job entity.InferenceIngestionJob, candidate entity.InferenceIngestionCandidate) {
	requestID := strings.TrimSpace(candidate.ID)
	if requestID == "" {
		requestID = generateID()
	}

	go func() {
		if limiter := service.StudyServiceDispatchSemaphore; limiter != nil {
			limiter <- struct{}{}
			defer func() {
				<-limiter
			}()
		}

		if err := service.dispatchRetrievedCandidateToStudyService(context.Background(), job, candidate, requestID); err != nil {
			log.Printf("[Ingestion dispatch] final dispatch failure candidate_id=%s ingestion_job_id=%s request_id=%s err=%v",
				candidate.ID,
				job.ID,
				requestID,
				err,
			)
		}
	}()
}

func (service *InferenceCommandService) dispatchRetrievedCandidateToStudyService(ctx context.Context, job entity.InferenceIngestionJob, candidate entity.InferenceIngestionCandidate, requestID string) error {
	dispatchRequest, err := service.BuildStudyServiceDispatchRequest(ctx, types.BuildStudyServiceDispatchRequestInput{
		IngestionJob: job,
		Candidate:    candidate,
		RequestID:    &requestID,
	})
	if err != nil {
		ObserveStudyServiceDispatchAttempt("permanent_error", 0)
		if persistErr := service.persistDispatchFailure(candidate.ID, err); persistErr != nil {
			log.Printf("[Ingestion dispatch] cannot persist dispatch failure candidate_id=%s ingestion_job_id=%s request_id=%s err=%v",
				candidate.ID, job.ID, requestID, persistErr,
			)
		}
		return err
	}

	retrySchedule := append([]time.Duration(nil), studyServiceDispatchRetrySchedule...)
	for attemptIndex, delay := range retrySchedule {
		if delay > 0 {
			time.Sleep(delay)
		}

		attemptCtx, cancel := context.WithTimeout(ctx, studyServiceDispatchAttemptTimeout)
		attemptStartedAt := time.Now()
		dispatchResponse, dispatchErr := service.DispatchStudy(attemptCtx, dispatchRequest)
		attemptDuration := time.Since(attemptStartedAt)
		cancel()

		if dispatchErr == nil {
			outcome := "success"
			if dispatchResponse.AlreadyPresent {
				outcome = "already_present"
			}

			ObserveStudyServiceDispatchAttempt(outcome, attemptDuration)
			if clearErr := service.InferenceCommandRepositoryInterface.UpdateCandidateDispatchState(repositoryTypes.UpdateCandidateDispatchState{
				ID:                candidate.ID,
				LastDispatchError: nil,
			}); clearErr != nil {
				log.Printf("[Ingestion dispatch] cannot clear dispatch error candidate_id=%s ingestion_job_id=%s request_id=%s err=%v",
					candidate.ID, job.ID, requestID, clearErr,
				)
			}
			service.recordQueuedProcessingDispatch(candidate, job, dispatchRequest, dispatchResponse)
			log.Printf("[Ingestion dispatch] dispatched candidate_id=%s ingestion_job_id=%s request_id=%s study_service_job_id=%s already_present=%t attempt=%d",
				candidate.ID,
				job.ID,
				requestID,
				dispatchResponse.JobID,
				dispatchResponse.AlreadyPresent,
				attemptIndex+1,
			)
			return nil
		}

		httpErr := &DispatchStudyHTTPError{}
		if errors.As(dispatchErr, &httpErr) {
			if !shouldRetryStudyServiceDispatchHTTPError(*httpErr) || attemptIndex == len(retrySchedule)-1 {
				ObserveStudyServiceDispatchAttempt("permanent_error", attemptDuration)
				if persistErr := service.persistDispatchFailure(candidate.ID, dispatchErr); persistErr != nil {
					log.Printf("[Ingestion dispatch] cannot persist dispatch failure candidate_id=%s ingestion_job_id=%s request_id=%s err=%v",
						candidate.ID, job.ID, requestID, persistErr,
					)
				}
				service.recordFailedProcessingDispatch(candidate, job, dispatchRequest, dispatchErr)
				return dispatchErr
			}

			ObserveStudyServiceDispatchAttempt("transient_error", attemptDuration)
			if httpErr.StatusCode == http.StatusTooManyRequests && httpErr.RetryAfter > 0 && attemptIndex+1 < len(retrySchedule) {
				retrySchedule[attemptIndex+1] = boundedRetryAfter(httpErr.RetryAfter)
			}
			log.Printf("[Ingestion dispatch] retrying transient HTTP error candidate_id=%s ingestion_job_id=%s request_id=%s attempt=%d status=%d err=%v",
				candidate.ID,
				job.ID,
				requestID,
				attemptIndex+1,
				httpErr.StatusCode,
				dispatchErr,
			)
			continue
		}

		ObserveStudyServiceDispatchAttempt("transient_error", attemptDuration)
		if attemptIndex == len(retrySchedule)-1 {
			if persistErr := service.persistDispatchFailure(candidate.ID, dispatchErr); persistErr != nil {
				log.Printf("[Ingestion dispatch] cannot persist dispatch failure candidate_id=%s ingestion_job_id=%s request_id=%s err=%v",
					candidate.ID, job.ID, requestID, persistErr,
				)
			}
			service.recordFailedProcessingDispatch(candidate, job, dispatchRequest, dispatchErr)
			return dispatchErr
		}

		log.Printf("[Ingestion dispatch] retrying network error candidate_id=%s ingestion_job_id=%s request_id=%s attempt=%d err=%v",
			candidate.ID,
			job.ID,
			requestID,
			attemptIndex+1,
			dispatchErr,
		)
	}

	return nil
}

func (service *InferenceCommandService) persistDispatchFailure(candidateID string, dispatchErr error) error {
	return service.InferenceCommandRepositoryInterface.UpdateCandidateDispatchState(repositoryTypes.UpdateCandidateDispatchState{
		ID:                candidateID,
		LastDispatchError: stringPointer(dispatchErr.Error()),
	})
}

func (service *InferenceCommandService) recordQueuedProcessingDispatch(candidate entity.InferenceIngestionCandidate, job entity.InferenceIngestionJob, dispatchRequest types.DispatchStudyRequest, dispatchResponse types.DispatchStudyResponse) {
	err := service.InferenceCommandRepositoryInterface.InsertInferenceIngestionProcessingJob(repositoryTypes.AddInferenceIngestionProcessingJob{
		ID:                generateID(),
		CandidateID:       candidate.ID,
		TenantID:          candidate.TenantID,
		ModelName:         job.ModelName,
		ModelVersion:      nonEmptyStringPointer(strings.TrimSpace(job.ModelVersion)),
		Modality:          nonEmptyStringPointer(strings.TrimSpace(dispatchRequest.Modality)),
		Status:            entity.InferenceIngestionProcessingJobStatusQueued,
		StudyServiceJobID: nonEmptyStringPointer(strings.TrimSpace(dispatchResponse.JobID)),
	})
	if err == nil {
		return
	}

	if err.Error() == apiError.DuplicateRecord {
		existing, queryErr := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionProcessingJobByCandidateModel(candidate.ID, job.ModelName)
		if queryErr != nil {
			log.Printf("[Ingestion dispatch] cannot load duplicate queued processing dispatch candidate_id=%s ingestion_job_id=%s err=%v",
				candidate.ID,
				job.ID,
				queryErr,
			)
			return
		}

		if existing.Status != entity.InferenceIngestionProcessingJobStatusFailed && existing.StudyServiceJobID != nil {
			return
		}

		updateErr := service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionProcessingJob(repositoryTypes.UpdateInferenceIngestionProcessingJob{
			ID:                existing.ID,
			Status:            entity.InferenceIngestionProcessingJobStatusQueued,
			ModelVersion:      nonEmptyStringPointer(strings.TrimSpace(job.ModelVersion)),
			Modality:          nonEmptyStringPointer(strings.TrimSpace(dispatchRequest.Modality)),
			StudyServiceJobID: nonEmptyStringPointer(strings.TrimSpace(dispatchResponse.JobID)),
			ErrorMessage:      nil,
		})
		if updateErr != nil {
			log.Printf("[Ingestion dispatch] cannot update duplicate queued processing dispatch candidate_id=%s ingestion_job_id=%s err=%v",
				candidate.ID,
				job.ID,
				updateErr,
			)
		}
		return
	}

	log.Printf("[Ingestion dispatch] cannot persist queued processing dispatch candidate_id=%s ingestion_job_id=%s err=%v",
		candidate.ID,
		job.ID,
		err,
	)
}

func (service *InferenceCommandService) recordFailedProcessingDispatch(candidate entity.InferenceIngestionCandidate, job entity.InferenceIngestionJob, dispatchRequest types.DispatchStudyRequest, dispatchErr error) {
	err := service.InferenceCommandRepositoryInterface.InsertInferenceIngestionProcessingJob(repositoryTypes.AddInferenceIngestionProcessingJob{
		ID:           generateID(),
		CandidateID:  candidate.ID,
		TenantID:     candidate.TenantID,
		ModelName:    job.ModelName,
		ModelVersion: nonEmptyStringPointer(strings.TrimSpace(job.ModelVersion)),
		Modality:     nonEmptyStringPointer(strings.TrimSpace(dispatchRequest.Modality)),
		Status:       entity.InferenceIngestionProcessingJobStatusFailed,
		ErrorMessage: stringPointer(dispatchErr.Error()),
	})
	if err == nil {
		return
	}

	if err.Error() == apiError.DuplicateRecord {
		existing, queryErr := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionProcessingJobByCandidateModel(candidate.ID, job.ModelName)
		if queryErr != nil {
			log.Printf("[Ingestion dispatch] cannot load duplicate failed processing dispatch candidate_id=%s ingestion_job_id=%s err=%v",
				candidate.ID,
				job.ID,
				queryErr,
			)
			return
		}

		if existing.Status != entity.InferenceIngestionProcessingJobStatusFailed && existing.StudyServiceJobID != nil {
			return
		}

		updateErr := service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionProcessingJob(repositoryTypes.UpdateInferenceIngestionProcessingJob{
			ID:           existing.ID,
			Status:       entity.InferenceIngestionProcessingJobStatusFailed,
			ModelVersion: nonEmptyStringPointer(strings.TrimSpace(job.ModelVersion)),
			Modality:     nonEmptyStringPointer(strings.TrimSpace(dispatchRequest.Modality)),
			ErrorMessage: stringPointer(dispatchErr.Error()),
		})
		if updateErr != nil {
			log.Printf("[Ingestion dispatch] cannot update duplicate failed processing dispatch candidate_id=%s ingestion_job_id=%s err=%v",
				candidate.ID,
				job.ID,
				updateErr,
			)
		}
		return
	}

	log.Printf("[Ingestion dispatch] cannot persist failed processing dispatch candidate_id=%s ingestion_job_id=%s err=%v",
		candidate.ID,
		job.ID,
		err,
	)
}

func (service *InferenceCommandService) retrieveStableCandidate(ctx context.Context, job entity.InferenceIngestionJob, candidate entity.InferenceIngestionCandidate) (candidateRetrievalResult, error) {
	isLocal, err := service.isStudyPresentLocally(ctx, candidate.StudyInstanceUID)
	if err != nil {
		return candidateRetrievalResult{}, err
	}

	if isLocal {
		return candidateRetrievalResult{
			Outcome:            candidateRetrievalOutcomeLocal,
			LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeLocal)),
		}, nil
	}

	if len(candidate.OrthancJobIDs) > 0 {
		return service.waitForCandidateRetrieval(ctx, candidate.StudyInstanceUID, []string(candidate.OrthancJobIDs))
	}

	retrieveResponses, err := service.OrthancCommandServiceInterface.RetrieveModalityStudyBySeries(ctx, orthancServiceTypes.RetrieveModalityStudyBySeries{
		TenantID:         job.TenantID,
		ModalityID:       job.DICOMModality,
		StudyInstanceUID: candidate.StudyInstanceUID,
		ModalityType:     "",
		UserID:           nil,
	})
	if err != nil {
		if err.Error() == apiError.DuplicateRecord {
			isLocal, localErr := service.isStudyPresentLocally(ctx, candidate.StudyInstanceUID)
			if localErr != nil {
				return candidateRetrievalResult{}, localErr
			}

			if isLocal {
				return candidateRetrievalResult{
					Outcome:            candidateRetrievalOutcomeLocal,
					LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeLocal)),
				}, nil
			}
		}

		return candidateRetrievalResult{}, err
	}

	jobIDs := extractOrthancJobIDs(retrieveResponses)
	if len(jobIDs) == 0 {
		return candidateRetrievalResult{
			Outcome:            candidateRetrievalOutcomeFailure,
			LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeFailure)),
			LastRetrievalError: stringPointer("Orthanc retrieve did not return any job IDs"),
		}, nil
	}

	err = service.InferenceCommandRepositoryInterface.SaveCandidateOrthancJobIDs(candidate.ID, jobIDs)
	if err != nil {
		return candidateRetrievalResult{}, err
	}

	return service.waitForCandidateRetrieval(ctx, candidate.StudyInstanceUID, jobIDs)
}

func (service *InferenceCommandService) waitForCandidateRetrieval(ctx context.Context, studyInstanceUID string, orthancJobIDs []string) (candidateRetrievalResult, error) {
	deadline := time.Now().Add(inferenceIngestionRetrievalTimeout)

	for {
		isLocal, err := service.isStudyPresentLocally(ctx, studyInstanceUID)
		if err != nil {
			return candidateRetrievalResult{}, err
		}

		if isLocal {
			return candidateRetrievalResult{
				Outcome:            candidateRetrievalOutcomeLocal,
				OrthancJobIDs:      orthancJobIDs,
				LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeLocal)),
			}, nil
		}

		jobs, err := service.OrthancQueryServiceInterface.GetJobsInfo(ctx, orthancJobIDs)
		if err != nil {
			return candidateRetrievalResult{}, err
		}

		if hasOrthancFailure(jobs) {
			return candidateRetrievalResult{
				Outcome:                   candidateRetrievalOutcomeFailure,
				OrthancJobIDs:             orthancJobIDs,
				LastRetrievalState:        stringPointer(string(candidateRetrievalOutcomeFailure)),
				LastRetrievalError:        orthancFailureDescription(jobs),
				LastRetrievalErrorDetails: marshalOrthancJobs(failedOrthancJobs(jobs)),
			}, nil
		}

		if hasOrthancSuccess(jobs) {
			isLocalAfterSuccess, localErr := service.isStudyPresentLocally(ctx, studyInstanceUID)
			if localErr != nil {
				return candidateRetrievalResult{}, localErr
			}

			outcome := candidateRetrievalOutcomeSuccess
			if isLocalAfterSuccess {
				outcome = candidateRetrievalOutcomeLocal
			}

			return candidateRetrievalResult{
				Outcome:                   outcome,
				OrthancJobIDs:             orthancJobIDs,
				LastRetrievalState:        stringPointer(string(outcome)),
				LastRetrievalErrorDetails: marshalOrthancJobs(jobs),
			}, nil
		}

		if time.Now().After(deadline) {
			return candidateRetrievalResult{
				Outcome:                   candidateRetrievalOutcomeTimeout,
				OrthancJobIDs:             orthancJobIDs,
				LastRetrievalState:        stringPointer(string(candidateRetrievalOutcomeTimeout)),
				LastRetrievalError:        stringPointer("Orthanc retrieval timed out"),
				LastRetrievalErrorDetails: marshalOrthancJobs(jobs),
			}, nil
		}

		select {
		case <-ctx.Done():
			return candidateRetrievalResult{}, ctx.Err()
		case <-time.After(inferenceIngestionRetrievalPollInterval):
		}
	}
}

func (service *InferenceCommandService) isStudyPresentLocally(ctx context.Context, studyInstanceUID string) (bool, error) {
	localResources, err := service.OrthancAPIInterface.FindLocalResources(ctx, orthancAPITypes.QueryLocalResourceRequest{
		Level: "Study",
		Query: orthancAPITypes.QueryLocalResource{
			StudyInstanceUID: studyInstanceUID,
		},
		Expand: true,
	})
	if err != nil {
		if err.Error() == apiError.MissingRecord {
			return false, nil
		}

		return false, err
	}

	return len(localResources) > 0, nil
}

// GenerateInferenceModelPredictRequest generates a predict request for inference model
func (service *InferenceCommandService) GenerateInferenceModelPredictRequest(ctx context.Context, tenantID, containerID string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictRequest, string, error) {
	// get inference model
	inferenceModel, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByContainer(ctx, tenantID, containerID)
	if err != nil {
		return dockerInferenceTypes.PredictRequest{}, "", err
	}

	// get container model info
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.PredictRequest{}, "", err
	}

	containerName := containerInfo.Name[1:] // remove "/" prefix

	modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(ctx, containerName) // remove "/" prefix
	if err != nil {
		return dockerInferenceTypes.PredictRequest{}, "", errors.New(apiError.DockerInferenceError)
	}

	seriesInstanceImages := map[int]map[int]string{}
	seriesInstanceMetadata := map[int]map[int]interface{}{}

	// check payload type
	// if supportedDicomTags = ["*"]
	if len(modelInfo.Data.SupportedDicomTags) == 1 && modelInfo.Data.SupportedDicomTags[0] == "*" {
		/// ---------------------- for DICOM images
		// purposely re-implementing the series ininstances loop because of potential refactors and differences vs metadata

		// get series instances
		var m = sync.Mutex{}
		eg, egCtx := errgroup.WithContext(ctx)

		// set limit
		eg.SetLimit(len(data.SeriesInstanceUIDs))

		for _, seriesInstanceUID := range data.SeriesInstanceUIDs {
			func(seriesInstanceUID string) {
				eg.Go(func() error {
					m.Lock()
					defer m.Unlock()

					// get instances
					instances, err := service.OrthancAPIInterface.GetDICOMWebSeriesInstances(egCtx, data.StudyInstanceUID, seriesInstanceUID)
					if err != nil {
						return err
					}

					var mInstance = sync.Mutex{}
					egInstance, egInstanceCtx := errgroup.WithContext(egCtx)

					// set limit
					egInstance.SetLimit(len(instances))

					for _, instance := range instances {
						func(instance map[string]interface{}) {
							egInstance.Go(func() error {
								seriesNumber := int(instance["00200011"].(map[string]interface{})["Value"].([]interface{})[0].(float64))
								sopInstanceUID := instance["00080018"].(map[string]interface{})["Value"].([]interface{})[0].(string)

								// assign by series number if doesnt exist yet
								if _, ok := seriesInstanceImages[seriesNumber]; !ok {
									seriesInstanceImages[seriesNumber] = make(map[int]string)
								}

								var sopInstanceNumber int

								// check for RTSTRUCT
								if instance["00080016"].(map[string]interface{})["Value"].([]interface{})[0].(string) == "1.2.840.10008.5.1.4.1.1.481.3" {
									sopInstanceNumber = seriesNumber // in RTSTRUCT, sopInstanceUID = seriesInstanceUID
								} else {
									// if a study got series but no instance, skip it
									if _, ok := instance["00200013"]; !ok {
										log.Println("[dicom-web] skipping because 00200013 is missing")
										return nil
									}

									sopInstanceNumber = int(instance["00200013"].(map[string]interface{})["Value"].([]interface{})[0].(float64))
								}

								// get instance file
								instanceFile, err := service.OrthancAPIInterface.RetrieveDICOMWebInstanceFile(egInstanceCtx, data.StudyInstanceUID, seriesInstanceUID, sopInstanceUID)
								if err != nil {
									return err
								}

								mInstance.Lock()
								seriesInstanceImages[seriesNumber][sopInstanceNumber] = base64.StdEncoding.EncodeToString(instanceFile) // convert to base64
								defer mInstance.Unlock()

								return nil
							})
						}(instance)
					}

					// wait for all goroutines to finish
					if err := egInstance.Wait(); err != nil {
						return err
					}

					return nil
				})
			}(seriesInstanceUID)
		}

		// wait for all goroutines to finish
		if err := eg.Wait(); err != nil {
			return dockerInferenceTypes.PredictRequest{}, "", err
		}

		// check if SeriesInstanceImages is empty
		if len(seriesInstanceImages) == 0 {
			log.Println("[predict] empty series instance images")
			return dockerInferenceTypes.PredictRequest{}, "", errors.New(apiError.InferenceError)
		}
	} else {
		/// ---------------------- for DICOM metadata
		// get allowed dicom tags
		var allowedDICOMTags []string

		for _, supportedDICOMTag := range modelInfo.Data.SupportedDicomTags {
			isAllowed := true
			for _, disallowedTag := range inferenceModel.DisallowedDICOMTags {
				if supportedDICOMTag == disallowedTag {
					isAllowed = false
					break
				}
			}

			if isAllowed {
				allowedDICOMTags = append(allowedDICOMTags, supportedDICOMTag)
			}
		}

		// get series instances
		var m = sync.Mutex{}
		eg, egCtx := errgroup.WithContext(ctx)

		// set limit
		eg.SetLimit(len(data.SeriesInstanceUIDs))

		for _, seriesInstanceUID := range data.SeriesInstanceUIDs {
			func(seriesInstanceUID string) {
				eg.Go(func() error {
					m.Lock()
					defer m.Unlock()

					// get instances
					instances, err := service.OrthancAPIInterface.GetDICOMWebSeriesInstances(egCtx, data.StudyInstanceUID, seriesInstanceUID)
					if err != nil {
						return err
					}

					var mInstance = sync.Mutex{}
					egInstance, egInstanceCtx := errgroup.WithContext(egCtx)

					// set limit
					egInstance.SetLimit(len(instances))

					for _, instance := range instances {
						func(instance map[string]interface{}) {
							egInstance.Go(func() error {
								mInstance.Lock()
								defer mInstance.Unlock()

								seriesNumber := int(instance["00200011"].(map[string]interface{})["Value"].([]interface{})[0].(float64))
								sopInstanceUID := instance["00080018"].(map[string]interface{})["Value"].([]interface{})[0].(string)

								// assign by series number if doesnt exist yet
								if _, ok := seriesInstanceMetadata[seriesNumber]; !ok {
									seriesInstanceMetadata[seriesNumber] = make(map[int]interface{})
								}

								var sopInstanceNumber int
								var reservedTags []string

								// check for RTSTRUCT
								if instance["00080016"].(map[string]interface{})["Value"].([]interface{})[0].(string) == "1.2.840.10008.5.1.4.1.1.481.3" {
									sopInstanceNumber = seriesNumber // in RTSTRUCT, sopInstanceUID = seriesInstanceUID

									// assign reserved tags required for RTSTRUCT
									reservedTags = []string{"30060020", "30060039", "30060080", "300E0002"}
								} else {
									// if a study got series but no instance, skip it
									if _, ok := instance["00200013"]; !ok {
										log.Println("[dicom-web] skipping because 00200013 is missing")
										return nil
									}

									sopInstanceNumber = int(instance["00200013"].(map[string]interface{})["Value"].([]interface{})[0].(float64))

									// assign reserved tags
									reservedTags = []string{"7FE00010"}
								}

								// filter already added reserved tags (avoid duplicates)
								for _, tag := range reservedTags {
									if !slices.Contains(allowedDICOMTags, tag) {
										allowedDICOMTags = append(allowedDICOMTags, tag)
									}
								}

								// get instance metadata
								instanceMetadata, err := service.OrthancAPIInterface.RetrieveDICOMWebInstanceMetadata(egInstanceCtx, data.StudyInstanceUID, seriesInstanceUID, sopInstanceUID)
								if err != nil {
									return err
								}

								// prepare allowed instance metadata
								allowedInstanceMetadata := map[string]interface{}{}

								for _, instanceMetadataMap := range instanceMetadata {
									for _, allowedDICOMTag := range allowedDICOMTags {
										if _, ok := instanceMetadataMap[allowedDICOMTag]; ok {
											allowedInstanceMetadata[allowedDICOMTag] = instanceMetadataMap[allowedDICOMTag]
										}
									}

									// iterate all tags and convert BulkDataURI to InlineBinary
									for key := range allowedInstanceMetadata {
										if metadata, ok := allowedInstanceMetadata[key].(map[string]interface{}); ok {
											if bulkDataURI, hasBulkData := metadata["BulkDataURI"].(string); hasBulkData {
												// convert bulk data URI to inline binary
												inlineBinary, err := dicomUtils.ConvertBulkDataURIToInlineBinary(bulkDataURI)
												if err != nil {
													return err
												}

												metadata["InlineBinary"] = inlineBinary
												delete(metadata, "BulkDataURI")
											}
										}
									}
								}

								seriesInstanceMetadata[seriesNumber][sopInstanceNumber] = allowedInstanceMetadata
								return nil
							})
						}(instance)
					}

					// wait for all goroutines to finish
					if err := egInstance.Wait(); err != nil {
						return err
					}

					return nil
				})
			}(seriesInstanceUID)
		}

		// wait for all goroutines to finish
		if err := eg.Wait(); err != nil {
			return dockerInferenceTypes.PredictRequest{}, "", err
		}

		// check if SeriesInstanceMetadata is empty
		if len(seriesInstanceMetadata) == 0 {
			log.Println("[predict] empty series instance metadata")
			return dockerInferenceTypes.PredictRequest{}, "", errors.New(apiError.InferenceError)
		}
	}

	// create predict request
	predictRequest := dockerInferenceTypes.PredictRequest{
		SeriesInstanceImages:   seriesInstanceImages,
		SeriesInstanceMetadata: seriesInstanceMetadata,
		AdditionalMetadata:     data.AdditionalMetadata,
		OutputMode:             dockerInferenceTypes.OutputMode(inferenceModel.OutputMode),
	}

	// override OutputMode to JSON if ForceJSON is true
	if data.ForceJSON != nil && *data.ForceJSON {
		predictRequest.OutputMode = dockerInferenceTypes.OutputModeJSON
	}

	return predictRequest, containerName, nil
}

// ImportInferenceIngestionJobs imports inference ingestion jobs
func (service *InferenceCommandService) ImportInferenceIngestionJobs(ctx context.Context, data []types.CreateInferenceIngestionJob) error {
	// add inference ingestion jobs
	for _, job := range data {
		jobConfig, err := normalizeIngestionJobConfig(job.StabilityMinutes, job.RecentWindowMinutes, job.MissingPollsThreshold, job.StudyTimeStart, job.StudyTimeEnd)
		if err != nil {
			return err
		}

		err = service.InferenceCommandRepositoryInterface.InsertInferenceIngestionJob(repositoryTypes.CreateInferenceIngestionJob{
			ID:                     generateID(),
			TenantID:               job.TenantID,
			DICOMModality:          job.DICOMModality,
			ContainerID:            job.ContainerID,
			ModelID:                job.ModelID,
			ModelName:              job.ModelName,
			ModelVersion:           job.ModelVersion,
			Modalities:             job.Modalities,
			RecentWindowMinutes:    jobConfig.RecentWindowMinutes,
			StabilityMinutes:       jobConfig.StabilityMinutes,
			MissingPollsThreshold:  jobConfig.MissingPollsThreshold,
			StudyTimeStart:         jobConfig.StudyTimeStart,
			StudyTimeEnd:           jobConfig.StudyTimeEnd,
			ScheduleStartTimestamp: time.Unix(int64(job.ScheduleStartTimestamp), 0),
			ScheduleEndTimestamp:   time.Unix(int64(job.ScheduleEndTimestamp), 0),
			Status:                 entity.InferenceIngestionJobStatusRunning, // default: RUNNING,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// PredictInferenceModel predicts an inference model
func (service *InferenceCommandService) PredictInferenceModel(ctx context.Context, tenantID, containerID string, userID *string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictResponse, error) {
	// TODO: remove this
	predictionStartTime := time.Now()

	predictRequest, containerName, err := service.GenerateInferenceModelPredictRequest(ctx, tenantID, containerID, data)
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

	// predict
	predictionResult, err := service.DockerInferenceAPIInterface.Predict(ctx, containerName, predictRequest)
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

	// TODO: remove this
	predictionEndTime := time.Since(predictionStartTime)
	log.Printf("[prediction] predict call took %f seconds", predictionEndTime.Seconds())

	// log to elasticsearch
	if userID != nil {
		go func() {
			user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, tenantID, *userID)
			if err != nil {
				log.Println(err)
				return
			}

			tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, tenantID)
			if err != nil {
				log.Println(err)
				return
			}

			// Get inference model data for logging
			inferenceModel, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByContainer(ctx, tenantID, containerID)
			if err != nil {
				log.Println(err)
				return
			}

			// get model info
			modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(ctx, containerName)
			if err != nil {
				log.Println(err)
				return
			}

			modelID := modelInfo.Data.ModelID
			if len(modelID) == 0 {
				modelID = modelInfo.Data.ModelName
			}

			_, err = service.ElasticsearchCommandServiceInterface.CreatePredictInferenceModelLog(ctx, elasticsearchTypes.CreatePredictInferenceModelLog{
				TenantID:           tenant.ID,
				TenantName:         tenant.Name,
				UserID:             user.ID,
				Email:              user.Email,
				Name:               user.Name,
				ContainerID:        containerID,
				ContainerName:      containerName,
				InferenceModelID:   inferenceModel.ID,
				InferenceModelName: inferenceModel.Name,
				DockerImage:        inferenceModel.DockerImage,
				Model:              fmt.Sprintf("%s-%s", modelID, modelInfo.Data.Version), // {modelID/modelName-version}
				StudyInstanceUID:   data.StudyInstanceUID,
				SeriesInstanceUIDs: data.SeriesInstanceUIDs,
				AdditionalMetadata: data.AdditionalMetadata,
			})
			if err != nil {
				log.Println(err)
				return
			}
		}()
	}

	return predictionResult, nil
}

// RemoveInferenceModel deletes an inference model
func (service *InferenceCommandService) RemoveInferenceModel(ctx context.Context, ID string) error {
	// get inference model
	inferenceModel, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByID(ctx, ID)
	if err != nil {
		return err
	}

	// force remove container
	err = service.DockerSDKInterface.RemoveContainer(ctx, inferenceModel.ContainerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// delete inference model
	err = service.InferenceCommandRepositoryInterface.DeleteInferenceModel(ctx, ID)
	if err != nil {
		return err
	}

	// delete inference ingestion jobs related
	err = service.InferenceCommandRepositoryInterface.DeleteInferenceIngestionJobByContainerID(inferenceModel.TenantID, inferenceModel.ContainerID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveInferenceIngestionJob removes an inference ingestion job
func (service *InferenceCommandService) RemoveInferenceIngestionJob(ctx context.Context, ID string) error {
	err := service.InferenceCommandRepositoryInterface.DeleteInferenceIngestionJob(ID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveOnboardingModelQuestionnaireAnswer removes an onboarding model questionnaire answer
func (service *InferenceCommandService) RemoveOnboardingModelQuestionnaireAnswer(ctx context.Context, ID string) error {
	err := service.InferenceCommandRepositoryInterface.DeleteOnboardingModelQuestionnaireAnswer(ctx, ID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveModelFeedback removes model feedback
func (service *InferenceCommandService) RemoveModelFeedback(ctx context.Context, data types.RemoveModelFeedback) error {
	// get model feedback
	modelFeedback, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackByUserModelID(ctx, repositoryTypes.GetModelFeedbackByUserModelID{
		TenantID: data.TenantID,
		UserID:   data.UserID,
		ModelID:  data.ModelID,
	})
	if err != nil {
		return err
	}

	// delete model feedback
	err = service.InferenceCommandRepositoryInterface.DeleteModelFeedback(ctx, modelFeedback.ID)
	if err != nil {
		return err
	}

	// get model feedback answers
	modelFeedbackAnswers, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackAnswersByFeedbackID(ctx, modelFeedback.ID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return err
	}

	// delete model feedback answers
	for _, modelFeedbackAnswer := range modelFeedbackAnswers {
		err = service.InferenceCommandRepositoryInterface.DeleteModelFeedbackAnswer(ctx, modelFeedbackAnswer.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

// RestartInferenceModelContainer restarts an inference model container
func (service *InferenceCommandService) RestartInferenceModelContainer(ctx context.Context, containerID string) error {
	err := service.DockerSDKInterface.RestartContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	return nil
}

// StartInferenceModelContainer starts an inference model container
func (service *InferenceCommandService) StartInferenceModelContainer(ctx context.Context, containerID string) error {
	err := service.DockerSDKInterface.StartContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	return nil
}

// StartInferenceIngestionJob starts an inference ingestion job
func (service *InferenceCommandService) StartInferenceIngestionJob(ctx context.Context, jobID string) error {
	err := service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJobStatus(jobID, entity.InferenceIngestionJobStatusRunning)
	if err != nil {
		return err
	}

	return nil
}

// StopInferenceModelContainer stops an inference model container
func (service *InferenceCommandService) StopInferenceModelContainer(ctx context.Context, containerID string) error {
	err := service.DockerSDKInterface.StopContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	return nil
}

// StopInferenceIngestionJob stops an inference ingestion job
func (service *InferenceCommandService) StopInferenceIngestionJob(ctx context.Context, jobID string) error {
	err := service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJobStatus(jobID, entity.InferenceIngestionJobStatusStopped)
	if err != nil {
		return err
	}

	return nil
}

// UpdateInferenceModel updates an inference model
func (service *InferenceCommandService) UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error {
	// TODO: check and compare output mode if supported

	err := service.InferenceCommandRepositoryInterface.UpdateInferenceModel(ctx, repositoryTypes.UpdateInferenceModel{
		ID:                  data.ID,
		DisallowedDICOMTags: data.DisallowedDICOMTags,
		OutputMode:          data.OutputMode,
	})
	if err != nil {
		return err
	}

	return nil
}

// UpdateInferenceIngestionJob updates an inference ingestion job
func (service *InferenceCommandService) UpdateInferenceIngestionJob(ctx context.Context, data types.UpdateInferenceIngestionJob) error {
	jobConfig, err := normalizeIngestionJobConfig(data.StabilityMinutes, data.RecentWindowMinutes, data.MissingPollsThreshold, data.StudyTimeStart, data.StudyTimeEnd)
	if err != nil {
		return err
	}

	err = service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJob(repositoryTypes.UpdateInferenceIngestionJob{
		ID:                     data.ID,
		Modalities:             data.Modalities,
		RecentWindowMinutes:    jobConfig.RecentWindowMinutes,
		StabilityMinutes:       jobConfig.StabilityMinutes,
		MissingPollsThreshold:  jobConfig.MissingPollsThreshold,
		StudyTimeStart:         jobConfig.StudyTimeStart,
		StudyTimeEnd:           jobConfig.StudyTimeEnd,
		ScheduleStartTimestamp: time.Unix(int64(data.ScheduleStartTimestamp), 0),
		ScheduleEndTimestamp:   time.Unix(int64(data.ScheduleEndTimestamp), 0),
	})
	if err != nil {
		return err
	}

	return nil
}

// TODO: check if needed
// UpdateInferenceModelContainerID updates the container ID of an inference model
func (service *InferenceCommandService) UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error {
	err := service.InferenceCommandRepositoryInterface.UpdateInferenceModelContainerID(ctx, ID, containerID)
	if err != nil {
		return err
	}

	return nil
}

// UpdateModelFeedback updates model feedback
func (service *InferenceCommandService) UpdateModelFeedback(ctx context.Context, data types.UpdateModelFeedback) error {
	modelFeedbackID := data.ID

	if modelFeedbackID == nil {
		modelFeedbackIDStr := generateID()
		modelFeedbackID = &modelFeedbackIDStr
	}

	err := service.InferenceCommandRepositoryInterface.UpsertModelFeedback(ctx, repositoryTypes.UpsertModelFeedback{
		ID:               *modelFeedbackID,
		TenantID:         data.TenantID,
		InferenceModelID: data.InferenceModelID,
		UserID:           data.UserID,
		ModelID:          data.ModelID,
		FeedbackType:     data.FeedbackType,
	})
	if err != nil {
		return err
	}

	// delete exiting feedback answers
	// get model feedback answers
	modelFeedbackAnswers, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackAnswersByFeedbackID(ctx, *modelFeedbackID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return err
	}

	// delete model feedback answers
	for _, modelFeedbackAnswer := range modelFeedbackAnswers {
		err = service.InferenceCommandRepositoryInterface.DeleteModelFeedbackAnswer(ctx, modelFeedbackAnswer.ID)
		if err != nil {
			return err
		}
	}

	// add model feedback answers
	for _, answer := range data.ModelFeedbackAnswers {
		err = service.InferenceCommandRepositoryInterface.InsertModelFeedbackAnswer(ctx, repositoryTypes.AddModelFeedbackAnswer{
			ID:                     generateID(),
			ModelFeedbackID:        *modelFeedbackID,
			QuestionnaireID:        answer.QuestionnaireID,
			QuestionnaireQuestion:  answer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   answer.QuestionnaireAnswers,
		})
		if err != nil {
			return err
		}
	}

	return nil
}
