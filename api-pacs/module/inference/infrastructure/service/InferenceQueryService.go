package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceQueryService handles the Inference query service logic
type InferenceQueryService struct {
	repository.InferenceQueryRepositoryInterface
	repository.InferenceModelVersionRepositoryInterface
	repository.InferenceProcessingRunRepositoryInterface
	dockerTypes.DockerSDKInterface
	dockerInferenceTypes.DockerInferenceAPIInterface
	application.InferenceQuotaManagerInterface
	application.ProcessingResultProviderInterface
	// StudyResultsProvider reads study-service latest results for the viewer.
	StudyResultsProvider application.StudyResultsProviderInterface
	// ModelReportRenderer re-renders stored results through model containers.
	ModelReportRenderer dockerInferenceTypes.ModelReportRendererInterface
	// ReportRenderTimeout bounds one render call (default 60s).
	ReportRenderTimeout time.Duration
	// ReportMaxBytes caps the render response body (default 32 MiB).
	ReportMaxBytes int64

	// availableModels remembers the last good model info per running
	// container, so a container too busy to answer stays in the list.
	availableModels availableModelCache
	// AvailableModelStore keeps that last good model info across api-pacs
	// restarts. Nil keeps it in memory only.
	AvailableModelStore AvailableModelInfoStore
	// FeedbackUsers resolves voter names for the admin feedback list. Nil (or
	// a failed lookup) leaves names and emails null.
	FeedbackUsers PredictionsUserDirectory
}

// AvailableModelInfoStore persists the last good model info per container
// as JSON, keyed by container ID.
type AvailableModelInfoStore interface {
	LoadAvailableModelInfo(ctx context.Context, containerID string) ([]byte, bool, error)
	SaveAvailableModelInfo(ctx context.Context, containerID string, info []byte) error
	// DeleteAvailableModelInfoExcept removes the info of every container not in containerIDs.
	DeleteAvailableModelInfoExcept(ctx context.Context, containerIDs []string) error
}

// availableModelStoreTimeout bounds one store call, so a slow database never
// holds up the model list.
const availableModelStoreTimeout = 2 * time.Second

// availableModelCache holds the last successful model info per container.
// It is keyed by container ID, so a recreated container never reuses old info.
type availableModelCache struct {
	mu      sync.Mutex
	entries map[string]types.GetInferenceAvailableModelResult
	// encoded is the JSON last stored per container, to persist only changes.
	encoded map[string][]byte
	// persistedSet is the registered container set last applied to the store.
	persistedSet string
	// storePausedUntil skips the store for a while after it fails (e.g. its
	// table is not created yet), so the model list does not log every call.
	storePausedUntil time.Time
}

const availableModelStorePause = 5 * time.Minute

func (cache *availableModelCache) storeUsable(now time.Time) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return !now.Before(cache.storePausedUntil)
}

func (cache *availableModelCache) pauseStore(now time.Time) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.storePausedUntil = now.Add(availableModelStorePause)
}

// store remembers result and reports whether its JSON differs from what was
// stored before for that container. A nil encoded always counts as a change.
func (cache *availableModelCache) store(result types.GetInferenceAvailableModelResult, encoded []byte) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[string]types.GetInferenceAvailableModelResult)
		cache.encoded = make(map[string][]byte)
	}
	cache.entries[result.ContainerID] = result
	changed := encoded == nil || !bytes.Equal(cache.encoded[result.ContainerID], encoded)
	cache.encoded[result.ContainerID] = encoded
	return changed
}

// claimPersistedSet reports whether set differs from the set last applied to
// the store, and records it.
func (cache *availableModelCache) claimPersistedSet(set string) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.persistedSet == set {
		return false
	}
	cache.persistedSet = set
	return true
}

func (cache *availableModelCache) forgetPersistedSet() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.persistedSet = ""
}

// retain drops the entries of containers that are no longer registered, so
// the cache does not grow with every redeploy or GPU move (new container ID).
func (cache *availableModelCache) retain(containerIDs map[string]struct{}) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for containerID := range cache.entries {
		if _, ok := containerIDs[containerID]; !ok {
			delete(cache.entries, containerID)
			delete(cache.encoded, containerID)
		}
	}
}

func (cache *availableModelCache) size() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return len(cache.entries)
}

func (cache *availableModelCache) load(containerID string) (types.GetInferenceAvailableModelResult, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	result, ok := cache.entries[containerID]
	return result, ok
}

// rememberModelInfo keeps result as the container's last good info, in memory
// and, when it changed, in the store.
func (service *InferenceQueryService) rememberModelInfo(ctx context.Context, result types.GetInferenceAvailableModelResult) {
	if service.AvailableModelStore == nil || !service.availableModels.storeUsable(time.Now()) {
		service.availableModels.store(result, nil)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		log.Printf("encode model info for %s: %v", result.ContainerID, err)
		service.availableModels.store(result, nil)
		return
	}
	if !service.availableModels.store(result, encoded) {
		return
	}
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), availableModelStoreTimeout)
	defer cancel()
	if err := service.AvailableModelStore.SaveAvailableModelInfo(storeCtx, result.ContainerID, encoded); err != nil {
		log.Printf("save model info for %s: %v", result.ContainerID, err)
		service.availableModels.pauseStore(time.Now())
		// Save again on the next good answer after the pause.
		service.availableModels.store(result, nil)
	}
}

// lastModelInfo returns the container's last good info: from memory, or from
// the store after an api-pacs restart.
func (service *InferenceQueryService) lastModelInfo(ctx context.Context, containerID string) (types.GetInferenceAvailableModelResult, bool) {
	if cached, ok := service.availableModels.load(containerID); ok {
		return cached, true
	}
	if service.AvailableModelStore == nil || !service.availableModels.storeUsable(time.Now()) {
		return types.GetInferenceAvailableModelResult{}, false
	}
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), availableModelStoreTimeout)
	defer cancel()
	encoded, ok, err := service.AvailableModelStore.LoadAvailableModelInfo(storeCtx, containerID)
	if err != nil {
		log.Printf("load stored model info for %s: %v", containerID, err)
		service.availableModels.pauseStore(time.Now())
		return types.GetInferenceAvailableModelResult{}, false
	}
	if !ok {
		return types.GetInferenceAvailableModelResult{}, false
	}
	var stored types.GetInferenceAvailableModelResult
	if err := json.Unmarshal(encoded, &stored); err != nil || stored.ContainerID != containerID {
		log.Printf("stored model info for %s is unreadable: %v", containerID, err)
		return types.GetInferenceAvailableModelResult{}, false
	}
	service.availableModels.store(stored, encoded)
	return stored, true
}

// retainModelInfo drops the remembered info of containers that are no longer
// registered. The store is updated only when the registered set changes.
func (service *InferenceQueryService) retainModelInfo(ctx context.Context, registered map[string]struct{}) {
	service.availableModels.retain(registered)
	if service.AvailableModelStore == nil || !service.availableModels.storeUsable(time.Now()) {
		return
	}
	containerIDs := make([]string, 0, len(registered))
	for containerID := range registered {
		containerIDs = append(containerIDs, containerID)
	}
	sort.Strings(containerIDs)
	if !service.availableModels.claimPersistedSet("set:" + strings.Join(containerIDs, ",")) {
		return
	}
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), availableModelStoreTimeout)
	defer cancel()
	if err := service.AvailableModelStore.DeleteAvailableModelInfoExcept(storeCtx, containerIDs); err != nil {
		log.Printf("drop stored model info of removed containers: %v", err)
		service.availableModels.pauseStore(time.Now())
		service.availableModels.forgetPersistedSet()
	}
}

// GetInferenceQuota returns the current tenant/user allowance and active work.
func (service *InferenceQueryService) GetInferenceQuota(ctx context.Context, tenantID, userID string) (types.InferenceQuotaStatus, error) {
	if service.InferenceQuotaManagerInterface == nil {
		return types.InferenceQuotaStatus{}, errors.New(apiError.InferenceQuotaUnavailable)
	}
	return service.InferenceQuotaManagerInterface.Status(ctx, tenantID, userID)
}

const (
	defaultWorklistPageLimit            = 25
	maximumWorklistPageLimit            = 100
	inferenceModelInspectionConcurrency = 4
)

// GetContainerInfo returns the container runtime information.
func (service *InferenceQueryService) GetContainerInfo(ctx context.Context, containerID string) (types.GetContainerInfoResult, error) {
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return types.GetContainerInfoResult{}, errors.New(apiError.DockerError)
	}

	return types.GetContainerInfoResult{
		ID:         containerInfo.ID,
		Name:       strings.TrimPrefix(containerInfo.Name, "/"),
		Lifecycle:  containerInfo.Lifecycle,
		StartedAt:  containerInfo.StartedAt,
		FinishedAt: containerInfo.FinishedAt,
	}, nil
}

// GetInferenceModels returns the inference models
func (service *InferenceQueryService) GetInferenceModels(ctx context.Context, tenantID string) ([]types.GetInferenceModelResult, error) {
	inferenceModels, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, errors.New(apiError.FirestoreError)
	}

	eg, egCtx := errgroup.WithContext(ctx)
	inferenceModelsResult := make([]types.GetInferenceModelResult, len(inferenceModels))
	eg.SetLimit(inferenceModelInspectionConcurrency)

	for index, inferenceModel := range inferenceModels {
		index, inferenceModel := index, inferenceModel
		eg.Go(func() error {
			result := types.GetInferenceModelResult{
				ID:       inferenceModel.ID,
				TenantID: inferenceModel.TenantID,
				Container: types.GetContainerInfoResult{
					ID:        inferenceModel.ContainerID,
					Lifecycle: types.InferenceModelLifecycleUnknown,
				},
				Name:                inferenceModel.Name,
				DockerImage:         inferenceModel.DockerImage,
				Envs:                inferenceModel.Envs,
				DisallowedDICOMTags: inferenceModel.DisallowedDICOMTags,
				OutputMode:          inferenceModel.OutputMode,
				CreatedAt:           time.Unix(int64(inferenceModel.CreatedAt), 0),
				UpdatedAt:           time.Unix(int64(inferenceModel.UpdatedAt), 0),
			}

			containerInfo, err := service.DockerSDKInterface.GetContainerInfo(egCtx, inferenceModel.ContainerID)
			if err != nil {
				if egCtx.Err() != nil {
					return egCtx.Err()
				}

				log.Printf("[Inference models] cannot inspect container model_id=%s container_id=%s err=%v", inferenceModel.ID, inferenceModel.ContainerID, err)
				inferenceModelsResult[index] = result
				return nil
			}

			result.Container = types.GetContainerInfoResult{
				ID:         containerInfo.ID,
				Name:       strings.TrimPrefix(containerInfo.Name, "/"),
				Lifecycle:  containerInfo.Lifecycle,
				StartedAt:  containerInfo.StartedAt,
				FinishedAt: containerInfo.FinishedAt,
			}
			inferenceModelsResult[index] = result
			return nil
		})
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return inferenceModelsResult, nil
}

// GetInferenceModelVersions resolves the artifact family from the tenant-owned
// registration, then returns its global immutable catalog entries.
func (service *InferenceQueryService) GetInferenceModelVersions(
	ctx context.Context,
	data types.GetInferenceModelVersions,
) ([]types.GetInferenceModelVersionResult, error) {
	if service.InferenceModelVersionRepositoryInterface == nil {
		return nil, errors.New(apiError.InferenceUpgradeUnavailable)
	}
	data.TenantID, data.ModelID = strings.TrimSpace(data.TenantID), strings.TrimSpace(data.ModelID)
	if data.TenantID == "" || data.ModelID == "" {
		return nil, errors.New(apiError.InvalidPayload)
	}
	registered, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByID(ctx, data.ModelID)
	if err != nil {
		return nil, err
	}
	if registered.TenantID != data.TenantID {
		return nil, errors.New(apiError.MissingRecord)
	}

	artifactModelID := ""
	activeCatalogVersionID := ""
	if registered.Deployment != nil {
		artifactModelID = strings.TrimSpace(registered.Deployment.ArtifactModelID)
		activeCatalogVersionID = strings.TrimSpace(registered.Deployment.CatalogVersionID)
	}
	if artifactModelID == "" {
		container, inspectErr := service.DockerSDKInterface.GetContainerInfo(ctx, registered.ContainerID)
		if inspectErr != nil {
			return nil, errors.New(apiError.DockerError)
		}
		modelInfo, infoErr := service.DockerInferenceAPIInterface.GetModelInfo(
			ctx, strings.TrimPrefix(strings.TrimSpace(container.Name), "/"),
		)
		if infoErr != nil || strings.TrimSpace(modelInfo.Data.ModelID) == "" {
			return nil, errors.New(apiError.DockerInferenceError)
		}
		artifactModelID = strings.TrimSpace(modelInfo.Data.ModelID)
	}

	versions, err := service.InferenceModelVersionRepositoryInterface.ListInferenceModelVersions(ctx, artifactModelID)
	if err != nil {
		return nil, err
	}
	result := make([]types.GetInferenceModelVersionResult, 0, len(versions))
	for _, version := range versions {
		result = append(result, types.GetInferenceModelVersionResult{
			Version: version,
			Active:  version.ID == activeCatalogVersionID || (activeCatalogVersionID == "" && version.ImageReference == registered.DockerImage),
		})
	}
	return result, nil
}

// GetInferenceModelInfo gets the inference model info
func (service *InferenceQueryService) GetInferenceModelInfo(ctx context.Context, containerID string) (dockerInferenceTypes.GetModelInfoResponse, error) {
	// get container name
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.GetModelInfoResponse{}, err
	}

	modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(ctx, strings.TrimPrefix(containerInfo.Name, "/"))
	if err != nil {
		return dockerInferenceTypes.GetModelInfoResponse{}, errors.New(apiError.DockerInferenceError)
	}

	return modelInfo, nil
}

// GetInferenceModelFacts gets the inference model facts
func (service *InferenceQueryService) GetInferenceModelFacts(ctx context.Context, containerID string) (dockerInferenceTypes.GetModelFactsResponse, error) {
	// get container name
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.GetModelFactsResponse{}, err
	}

	modelFacts, err := service.DockerInferenceAPIInterface.GetModelFacts(ctx, strings.TrimPrefix(containerInfo.Name, "/"))
	if err != nil {
		return dockerInferenceTypes.GetModelFactsResponse{}, errors.New(apiError.DockerInferenceError)
	}

	return modelFacts, nil
}

// GetInferenceAvailableModels gets the inference available models
func (service *InferenceQueryService) GetInferenceAvailableModels(ctx context.Context, tenantID string) ([]types.GetInferenceAvailableModelResult, error) {
	// get inference models
	inferenceModels, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}

	registered := make(map[string]struct{}, len(inferenceModels))
	for _, inferenceModel := range inferenceModels {
		registered[inferenceModel.ContainerID] = struct{}{}
	}
	service.retainModelInfo(ctx, registered)

	eg, egCtx := errgroup.WithContext(ctx)
	availableByIndex := make([]*types.GetInferenceAvailableModelResult, len(inferenceModels))
	eg.SetLimit(inferenceModelInspectionConcurrency)

	for index, inferenceModel := range inferenceModels {
		index, inferenceModel := index, inferenceModel
		eg.Go(func() error {
			containerInfo, err := service.DockerSDKInterface.GetContainerInfo(egCtx, inferenceModel.ContainerID)
			if err != nil {
				if egCtx.Err() != nil {
					return egCtx.Err()
				}

				log.Println(err)
				return nil // skip error
			}

			containerName := strings.TrimPrefix(containerInfo.Name, "/")

			// check if container id is set and running
			if len(inferenceModel.ContainerID) > 0 && containerInfo.Lifecycle == dockerTypes.LifecycleRunning {
				// get model info
				// A busy container may not answer in time: keep its last good info.
				useCached := func(err error) {
					log.Println(err)
					if cached, ok := service.lastModelInfo(egCtx, inferenceModel.ContainerID); ok {
						log.Printf("model info for %s unavailable, using last known info", containerName)
						cached.ContainerName = containerName
						cached.OutputMode = inferenceModel.OutputMode
						availableByIndex[index] = &cached
					}
				}

				modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(egCtx, containerName)
				if err != nil {
					if egCtx.Err() != nil {
						return egCtx.Err()
					}

					useCached(err)
					return nil
				}

				// get model facts
				modelFacts, err := service.DockerInferenceAPIInterface.GetModelFacts(egCtx, containerName)
				if err != nil {
					if egCtx.Err() != nil {
						return egCtx.Err()
					}

					useCached(err)
					return nil
				}

				result := types.GetInferenceAvailableModelResult{
					ContainerID:                   inferenceModel.ContainerID,
					ContainerName:                 containerName,
					ModelID:                       modelInfo.Data.ModelID,
					ModelName:                     modelInfo.Data.ModelName,
					ModelFacts:                    types.ModelFacts(modelFacts.Data),
					Version:                       modelInfo.Data.Version,
					Provenance:                    modelInfo.Data.Provenance,
					DicomTargetLevel:              modelInfo.Data.DicomTargetLevel,
					DicomUploadMin:                modelInfo.Data.DicomUploadMin,
					DicomUploadMax:                modelInfo.Data.DicomUploadMax,
					SupportedDicomModalities:      modelInfo.Data.SupportedDicomModalities,
					SupportedDicomTags:            modelInfo.Data.SupportedDicomTags,
					SupportedAdditionalMetadata:   modelInfo.Data.SupportedAdditionalMetadata,
					ApproveFeedbackQuestionnaires: modelInfo.Data.ApproveFeedbackQuestionnaires,
					RejectFeedbackQuestionnaires:  modelInfo.Data.RejectFeedbackQuestionnaires,
					OnboardingModelQuestionnaires: modelInfo.Data.OnboardingModelQuestionnaires,
					OutputMode:                    inferenceModel.OutputMode,
				}
				service.rememberModelInfo(egCtx, result)
				availableByIndex[index] = &result
			}

			return nil
		})
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	inferenceAvailableModels := make([]types.GetInferenceAvailableModelResult, 0, len(inferenceModels))
	for _, result := range availableByIndex {
		if result != nil {
			inferenceAvailableModels = append(inferenceAvailableModels, *result)
		}
	}

	return inferenceAvailableModels, nil
}

// GetInferenceIngestionJobs gets the inference ingestion jobs
func (controller *InferenceQueryService) GetInferenceIngestionJobs(ctx context.Context, tenantID string) ([]entity.InferenceIngestionJob, error) {
	res, err := controller.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobs(&tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}

	return res, nil
}

// GetInferenceIngestionCandidates gets ingestion candidates for debugging and operations
func (service *InferenceQueryService) GetInferenceIngestionCandidates(ctx context.Context, data types.GetInferenceIngestionCandidates) ([]entity.InferenceIngestionCandidate, error) {
	var status *entity.InferenceIngestionCandidateStatus
	if data.Status != nil {
		parsedStatus, ok := parseInferenceIngestionCandidateStatus(*data.Status)
		if !ok {
			return nil, errors.New(apiError.InvalidPayload)
		}

		status = &parsedStatus
	}

	res, err := service.InferenceQueryRepositoryInterface.ListInferenceIngestionCandidates(repositoryTypes.ListInferenceIngestionCandidates{
		TenantID:          data.TenantID,
		IngestionJobID:    data.IngestionJobID,
		StudyInstanceUID:  data.StudyInstanceUID,
		Status:            status,
		RetrievalFailures: data.RetrievalFailures,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}

	return res, nil
}

func parseInferenceIngestionCandidateStatus(status string) (entity.InferenceIngestionCandidateStatus, bool) {
	normalizedStatus := strings.ToUpper(status)

	switch entity.InferenceIngestionCandidateStatus(normalizedStatus) {
	case entity.InferenceIngestionCandidateStatusDiscovered,
		entity.InferenceIngestionCandidateStatusGrowing,
		entity.InferenceIngestionCandidateStatusStable,
		entity.InferenceIngestionCandidateStatusRetrievalQueued,
		entity.InferenceIngestionCandidateStatusRetrieved,
		entity.InferenceIngestionCandidateStatusDisappeared,
		entity.InferenceIngestionCandidateStatusFailed:
		return entity.InferenceIngestionCandidateStatus(normalizedStatus), true
	default:
		return "", false
	}
}

// GetWorklistStudyStatuses returns the current worklist snapshot for one
// authenticated tenant. The repository remains responsible for enforcing the
// tenant predicate; this layer normalizes input and maps persistence types into
// the public frontend contract.
func (service *InferenceQueryService) GetWorklistStudyStatuses(_ context.Context, data types.GetWorklistStudyStatuses) (types.WorklistStudyStatusPage, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	limit, err := normalizeWorklistPagination(tenantID, data.Limit, data.Offset)
	if err != nil {
		return types.WorklistStudyStatusPage{}, err
	}

	studyInstanceUIDs, valid := normalizeStudyInstanceUIDs(data.StudyInstanceUIDs)
	if !valid {
		return types.WorklistStudyStatusPage{}, errors.New(apiError.InvalidPayload)
	}

	repositoryPage, err := service.InferenceQueryRepositoryInterface.ListWorklistStudyStatuses(repositoryTypes.ListWorklistStudyStatuses{
		TenantID:          tenantID,
		StudyInstanceUIDs: studyInstanceUIDs,
		Limit:             limit,
		Offset:            data.Offset,
	})
	if err != nil {
		return types.WorklistStudyStatusPage{}, err
	}

	studies := make([]types.WorklistStudyStatus, 0, len(repositoryPage.Studies))
	for _, status := range repositoryPage.Studies {
		attentionReasons := status.AttentionReasons
		if attentionReasons == nil {
			attentionReasons = entity.InferenceIngestionProcessingRunAttentionReasons{}
		}

		studies = append(studies, types.WorklistStudyStatus{
			StudyInstanceUID:  status.StudyInstanceUID,
			IngestionStatus:   status.IngestionStatus,
			RetrievalState:    status.RetrievalState,
			RetrievalError:    status.RetrievalError,
			RunID:             status.RunID,
			RunNumber:         status.RunNumber,
			Trigger:           status.RunTrigger,
			Phase:             status.Phase,
			Outcome:           status.Outcome,
			AttentionRequired: status.AttentionRequired,
			AttentionReasons:  attentionReasons,
			ProcessingRunCounts: types.ProcessingRunCounts{
				Expected:  status.ExpectedModels,
				Pending:   status.PendingModels,
				Queued:    status.QueuedModels,
				Running:   status.RunningModels,
				Completed: status.CompletedModels,
				Failed:    status.FailedModels,
				Skipped:   status.SkippedModels,
				Cancelled: status.CancelledModels,
				Active:    status.ActiveModels,
			},
			Version:     status.Version,
			StartedAt:   status.StartedAt,
			CompletedAt: status.CompletedAt,
			UpdatedAt:   status.UpdatedAt,
		})
	}

	return types.WorklistStudyStatusPage{
		Studies: studies,
		WorklistPage: types.WorklistPage{
			Limit:   limit,
			Offset:  data.Offset,
			HasMore: repositoryPage.HasMore,
		},
	}, nil
}

func normalizeStudyInstanceUIDs(values []string) ([]string, bool) {
	if len(values) == 0 {
		return nil, true
	}

	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		uid := strings.TrimSpace(value)
		if uid == "" {
			return nil, false
		}
		if _, exists := seen[uid]; exists {
			continue
		}

		seen[uid] = struct{}{}
		normalized = append(normalized, uid)
	}

	return normalized, true
}

// GetStudyProcessingRunHistory returns newest-first runs and their frozen model
// executions for one tenant-scoped study.
func (service *InferenceQueryService) GetStudyProcessingRunHistory(ctx context.Context, data types.GetStudyProcessingRunHistory) (types.StudyProcessingRunHistoryPage, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	studyInstanceUID := strings.TrimSpace(data.StudyInstanceUID)
	limit, err := normalizeWorklistPagination(tenantID, data.Limit, data.Offset)
	if err != nil {
		return types.StudyProcessingRunHistoryPage{}, err
	}
	if studyInstanceUID == "" {
		return types.StudyProcessingRunHistoryPage{}, errors.New(apiError.InvalidPayload)
	}

	repositoryPage, err := service.InferenceProcessingRunRepositoryInterface.ListProcessingRunHistoryPage(ctx, repositoryTypes.ListInferenceIngestionProcessingRuns{
		TenantID: tenantID, StudyInstanceUID: studyInstanceUID, Limit: limit, Offset: data.Offset,
	})
	if err != nil {
		return types.StudyProcessingRunHistoryPage{}, err
	}

	runIDs := make([]string, 0, len(repositoryPage.Runs))
	for _, run := range repositoryPage.Runs {
		runIDs = append(runIDs, run.ID)
	}

	executions, err := service.InferenceProcessingRunRepositoryInterface.ListProcessingRunExecutionsByRunIDs(ctx, repositoryTypes.ListInferenceIngestionProcessingRunExecutions{
		TenantID: tenantID, ProcessingRunIDs: runIDs,
	})
	if err != nil {
		return types.StudyProcessingRunHistoryPage{}, err
	}

	executionsByRunID := make(map[string][]entity.InferenceIngestionProcessingJob, len(repositoryPage.Runs))
	for _, execution := range executions {
		if execution.ProcessingRunID == nil {
			continue
		}
		executionsByRunID[*execution.ProcessingRunID] = append(executionsByRunID[*execution.ProcessingRunID], execution)
	}

	runs := make([]types.ProcessingRunDetail, 0, len(repositoryPage.Runs))
	for _, run := range repositoryPage.Runs {
		runs = append(runs, buildProcessingRunDetail(run, executionsByRunID[run.ID]))
	}

	return types.StudyProcessingRunHistoryPage{
		Runs: runs,
		WorklistPage: types.WorklistPage{
			Limit: limit, Offset: data.Offset, HasMore: repositoryPage.HasMore,
		},
	}, nil
}

// GetProcessingRunDetail returns one run only when its ID belongs to the
// authenticated tenant, then loads the run's frozen model plan.
func (service *InferenceQueryService) GetProcessingRunDetail(ctx context.Context, data types.GetProcessingRunDetail) (types.ProcessingRunDetail, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	runID := strings.TrimSpace(data.RunID)
	if tenantID == "" || runID == "" {
		return types.ProcessingRunDetail{}, errors.New(apiError.InvalidPayload)
	}

	run, err := service.InferenceProcessingRunRepositoryInterface.SelectProcessingRun(ctx, tenantID, runID)
	if err != nil {
		return types.ProcessingRunDetail{}, err
	}

	executions, err := service.InferenceProcessingRunRepositoryInterface.ListProcessingRunExecutions(ctx, tenantID, runID)
	if err != nil {
		return types.ProcessingRunDetail{}, err
	}

	detail := buildProcessingRunDetail(run, executions)
	service.describeStudyGrowingWait(run, executions, &detail)
	return detail, nil
}

// GetProcessingRunExecutionResult returns an opaque result only after local
// tenant/run/execution ownership and upstream job correlation are validated.
func (service *InferenceQueryService) GetProcessingRunExecutionResult(ctx context.Context, data types.GetProcessingRunExecutionResult) (types.ProcessingRunExecutionResult, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	runID := strings.TrimSpace(data.RunID)
	executionID := strings.TrimSpace(data.ExecutionID)
	if tenantID == "" || runID == "" || executionID == "" {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InvalidPayload)
	}

	run, err := service.InferenceProcessingRunRepositoryInterface.SelectProcessingRun(ctx, tenantID, runID)
	if err != nil {
		return types.ProcessingRunExecutionResult{}, err
	}
	execution, err := service.InferenceProcessingRunRepositoryInterface.SelectProcessingRunExecutionByID(ctx, tenantID, runID, executionID)
	if err != nil {
		return types.ProcessingRunExecutionResult{}, err
	}
	if execution.Status != entity.InferenceIngestionProcessingJobStatusCompleted {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InferenceExecutionResultNotAvailable)
	}
	if execution.CompletedAt == nil || execution.StudyServiceJobID == nil || strings.TrimSpace(*execution.StudyServiceJobID) == "" {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InferenceExecutionResultInvalid)
	}
	if service.ProcessingResultProviderInterface == nil {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InferenceResultServiceUnavailable)
	}

	jobID := strings.TrimSpace(*execution.StudyServiceJobID)
	job, found, err := service.ProcessingResultProviderInterface.GetJobResultByID(ctx, tenantID, jobID)
	if err != nil {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InferenceResultServiceUnavailable)
	}
	if !found || !executionResultJobMatches(job.StudyServiceJob, run, execution, tenantID, runID, executionID, jobID) || !validExecutionResultJSON(job.ResultJSON) {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InferenceExecutionResultInvalid)
	}

	return types.ProcessingRunExecutionResult{
		RunID:            runID,
		ExecutionID:      executionID,
		StudyInstanceUID: run.StudyInstanceUID,
		ModelName:        execution.ModelName,
		ModelVersion:     execution.ModelVersion,
		Status:           execution.Status,
		CompletedAt:      *execution.CompletedAt,
		Result:           append(json.RawMessage(nil), job.ResultJSON...),
		SelectedViews:    normalizeSelectedViews(job.SelectedViews),
	}, nil
}

func executionResultJobMatches(job types.StudyServiceJob, run entity.InferenceIngestionProcessingRun, execution entity.InferenceIngestionProcessingJob, tenantID, runID, executionID, jobID string) bool {
	if strings.TrimSpace(job.JobID) != jobID ||
		strings.TrimSpace(job.StudyInstanceUID) != strings.TrimSpace(run.StudyInstanceUID) ||
		trimmedPointerValue(job.TenantID) != tenantID ||
		trimmedPointerValue(job.ProcessingRunID) != runID ||
		trimmedPointerValue(job.CandidateID) != strings.TrimSpace(execution.CandidateID) ||
		strings.TrimSpace(job.ModelName) != strings.TrimSpace(execution.ModelName) ||
		!strings.EqualFold(strings.TrimSpace(job.Status), string(entity.InferenceIngestionProcessingJobStatusCompleted)) {
		return false
	}

	processingExecutionID := trimmedPointerValue(job.ProcessingExecutionID)
	if run.RunTrigger == entity.InferenceIngestionProcessingRunTriggerAuto {
		// Automatic study-service jobs intentionally use their tenant/run/candidate/
		// model/job correlation and may omit the manual-only execution identity.
		// If a rolling-upgrade job supplies one, it must still match exactly.
		if processingExecutionID != "" && processingExecutionID != executionID {
			return false
		}
	} else if processingExecutionID != executionID {
		return false
	}

	localVersion := trimmedPointerValue(execution.ModelVersion)
	return localVersion == "" || trimmedPointerValue(job.ModelVersion) == localVersion
}

func validExecutionResultJSON(result json.RawMessage) bool {
	trimmed := bytes.TrimSpace(result)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && json.Valid(trimmed)
}

func normalizeWorklistPagination(tenantID string, limit, offset int) (int, error) {
	if tenantID == "" || limit < 0 || offset < 0 {
		return 0, errors.New(apiError.InvalidPayload)
	}
	if limit == 0 {
		limit = defaultWorklistPageLimit
	}
	if limit > maximumWorklistPageLimit {
		return 0, errors.New(apiError.MaximumLimitReached)
	}
	return limit, nil
}

func buildProcessingRunDetail(run entity.InferenceIngestionProcessingRun, executions []entity.InferenceIngestionProcessingJob) types.ProcessingRunDetail {
	attentionReasons := run.AttentionReasons
	if attentionReasons == nil {
		attentionReasons = entity.InferenceIngestionProcessingRunAttentionReasons{}
	}

	executionSummaries := make([]types.ProcessingRunExecutionSummary, 0, len(executions))
	for index := range executions {
		execution := &executions[index]
		executionSummaries = append(executionSummaries, types.ProcessingRunExecutionSummary{
			ExecutionID:  execution.ID,
			ModelName:    execution.ModelName,
			ModelVersion: execution.ModelVersion,
			Modality:     execution.Modality,
			Status:       execution.Status,
			ErrorMessage: execution.ErrorMessage,
			SkipReason:   execution.GetSkipReason(),
			StartedAt:    execution.StartedAt,
			CompletedAt:  execution.CompletedAt,
			UpdatedAt:    execution.UpdatedAt,
		})
	}

	aggregate := entity.AggregateInferenceIngestionProcessingRun(entity.InferenceIngestionProcessingRunAggregationInput{
		Run: run, Executions: executions,
	})

	var waitingReason types.ProcessingRunWaitingReason
	if processingRunIsStaleWaiting(run, aggregate.Counts) {
		waitingReason = types.ProcessingRunWaitingReasonStale
	}

	return types.ProcessingRunDetail{
		Cancellable:   len(entity.CancellableProcessingRunExecutions(run, executions)) > 0,
		CancelledAt:   run.CancelledAt,
		WaitingReason: waitingReason,
		ProcessingRunSummary: types.ProcessingRunSummary{
			RunID:             run.ID,
			StudyInstanceUID:  run.StudyInstanceUID,
			RunNumber:         run.RunNumber,
			Trigger:           run.RunTrigger,
			Phase:             run.Phase,
			Outcome:           run.Outcome,
			AttentionRequired: run.AttentionRequired,
			AttentionReasons:  attentionReasons,
			ProcessingRunCounts: types.ProcessingRunCounts{
				Expected:  aggregate.Counts.Expected,
				Pending:   aggregate.Counts.Pending,
				Queued:    aggregate.Counts.Queued,
				Running:   aggregate.Counts.Running,
				Completed: aggregate.Counts.Completed,
				Failed:    aggregate.Counts.Failed,
				Skipped:   aggregate.Counts.Skipped,
				Cancelled: aggregate.Counts.Cancelled,
				Active:    aggregate.Counts.Active,
			},
			Version:     run.Version,
			StartedAt:   run.StartedAt,
			CompletedAt: run.CompletedAt,
			CreatedAt:   run.CreatedAt,
			UpdatedAt:   run.UpdatedAt,
		},
		Executions: executionSummaries,
	}
}

// GetModelFeedBackByUser gets the model feedback by user
func (service *InferenceQueryService) GetModelFeedBackByUser(ctx context.Context, data types.GetModelFeedbackByUser) (types.GetModelFeedbackResult, error) {
	modelFeedback, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackByUserModelID(ctx, repositoryTypes.GetModelFeedbackByUserModelID{
		TenantID: data.TenantID,
		UserID:   data.UserID,
		ModelID:  data.ModelID,
	})
	if err != nil {
		return types.GetModelFeedbackResult{}, err
	}

	var modelFeedbackAnswersResult []types.ModelFeedbackAnswerResult

	// get model feedback answers
	modelFeedbackAnswers, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackAnswersByFeedbackID(ctx, modelFeedback.ID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return types.GetModelFeedbackResult{}, err
	}

	for _, modelFeedbackAnswer := range modelFeedbackAnswers {
		modelFeedbackAnswersResult = append(modelFeedbackAnswersResult, types.ModelFeedbackAnswerResult{
			ID:                     modelFeedbackAnswer.ID,
			ModelFeedbackID:        modelFeedbackAnswer.ModelFeedbackID,
			QuestionnaireID:        modelFeedbackAnswer.QuestionnaireID,
			QuestionnaireQuestion:  modelFeedbackAnswer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: modelFeedbackAnswer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   modelFeedbackAnswer.QuestionnaireAnswers,
		})
	}

	return types.GetModelFeedbackResult{
		ID:                   modelFeedback.ID,
		TenantID:             modelFeedback.TenantID,
		UserID:               modelFeedback.UserID,
		InferenceModelID:     modelFeedback.InferenceModelID,
		ModelID:              modelFeedback.ModelID,
		FeedbackType:         modelFeedback.FeedbackType,
		ModelFeedbackAnswers: modelFeedbackAnswersResult,
	}, nil
}

// GetOnboardingModelQuestionnaireAnswers gets the onboarding model questionnaire answers
func (service *InferenceQueryService) GetOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingModelQuestionnaireAnswer) ([]entity.OnboardingModelQuestionnaireAnswer, error) {
	res, err := service.InferenceQueryRepositoryInterface.SelectOnboardingModelQuestionnaireAnswers(ctx, repositoryTypes.GetOnboardingModelQuestionnaireAnswer{
		TenantID: data.TenantID,
		UserID:   data.UserID,
		ModelID:  data.ModelID,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}

	return res, nil
}
