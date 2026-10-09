package modelupgrade

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
)

const (
	testOldContainer = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testNewContainer = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testDigest       = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testSourceSHA    = "dddddddddddddddddddddddddddddddddddddddd"
	testModelSHA     = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	testWeightsSHA   = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

type fakeUpgradeRepository struct {
	mu                  sync.Mutex
	model               entity.InferenceModel
	attempts            map[string]entity.InferenceModelUpgrade
	beginErr            error
	beginBeforeError    bool
	getModelErr         error
	getModelErrors      []error
	getAttemptErrors    []error
	activateErrors      []error
	activateBeforeError bool
	finishErrors        []error
	finishBeforeError   bool
	finishCalls         int
	finishStarted       chan struct{}
	finishRelease       chan struct{}
	save                func(entity.InferenceModelUpgrade) error
	jobContainerID      string
	jobVersion          string
	retargetErr         error
	retargetCalls       []string
	activeJobs          []bool
	activeJobsErr       error
	activeJobCalls      int
	activeJobTenant     string
	activeJobModel      string
	activeJobVersion    string
	sharedContainers    map[string]bool
	sharedContainerErr  error
}

func (repository *fakeUpgradeRepository) HasOtherInferenceModelRegistrations(
	_ context.Context, _ string, containerID string,
) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.sharedContainerErr != nil {
		return false, repository.sharedContainerErr
	}
	return repository.sharedContainers[containerID], nil
}

func newFakeUpgradeRepository() *fakeUpgradeRepository {
	return &fakeUpgradeRepository{
		model: entity.InferenceModel{
			ID: "model-1", TenantID: "tenant-1", ContainerID: testOldContainer,
			Name: "Model", DockerImage: "heartwisehub/model:1.0.0", Envs: []string{"TOKEN=secret"}, OutputMode: entity.OutputModeJSON,
		},
		attempts:       make(map[string]entity.InferenceModelUpgrade),
		jobContainerID: testOldContainer,
		jobVersion:     "1.0.0",
	}
}

func (repository *fakeUpgradeRepository) RetargetInferenceIngestionJobs(_ context.Context, _ string, fromContainerID, toContainerID, modelVersion string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.retargetErr != nil {
		return repository.retargetErr
	}
	repository.retargetCalls = append(repository.retargetCalls, fromContainerID+"->"+toContainerID)
	if repository.jobContainerID == fromContainerID {
		repository.jobContainerID = toContainerID
		repository.jobVersion = modelVersion
	}
	return nil
}

func (repository *fakeUpgradeRepository) HasActiveInferenceProcessingJobs(
	_ context.Context, tenantID, modelName, modelVersion string,
) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.activeJobCalls++
	repository.activeJobTenant = tenantID
	repository.activeJobModel = modelName
	repository.activeJobVersion = modelVersion
	if repository.activeJobsErr != nil {
		return false, repository.activeJobsErr
	}
	if len(repository.activeJobs) == 0 {
		return false, nil
	}
	active := repository.activeJobs[0]
	if len(repository.activeJobs) > 1 {
		repository.activeJobs = repository.activeJobs[1:]
	}
	return active, nil
}

func (repository *fakeUpgradeRepository) BeginInferenceModelUpgrade(_ context.Context, attempt entity.InferenceModelUpgrade) (entity.InferenceModelUpgrade, entity.InferenceModel, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.beginErr != nil && !repository.beginBeforeError {
		return entity.InferenceModelUpgrade{}, entity.InferenceModel{}, repository.beginErr
	}
	if repository.model.ActiveUpgradeID != "" || repository.model.DeletionClaimID != "" {
		return entity.InferenceModelUpgrade{}, entity.InferenceModel{}, errors.New(apiError.DuplicateRecord)
	}
	attempt.ModelName = repository.model.Name
	attempt.Previous = entity.InferenceModelDeployment{
		ContainerID: repository.model.ContainerID, ImageReference: repository.model.DockerImage,
	}
	repository.model.ActiveUpgradeID = attempt.ID
	repository.attempts[attempt.ID] = attempt
	if repository.beginErr != nil {
		return entity.InferenceModelUpgrade{}, entity.InferenceModel{}, repository.beginErr
	}
	return attempt, repository.model, nil
}

func (repository *fakeUpgradeRepository) GetInferenceModelUpgrade(_ context.Context, tenantID, modelID, upgradeID string) (entity.InferenceModelUpgrade, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.getAttemptErrors) > 0 {
		err := repository.getAttemptErrors[0]
		repository.getAttemptErrors = repository.getAttemptErrors[1:]
		if err != nil {
			return entity.InferenceModelUpgrade{}, err
		}
	}
	attempt, exists := repository.attempts[upgradeID]
	if !exists || attempt.TenantID != tenantID || attempt.ModelID != modelID {
		return entity.InferenceModelUpgrade{}, errors.New(apiError.MissingRecord)
	}
	return attempt, nil
}

func (repository *fakeUpgradeRepository) GetInferenceModelForUpgrade(_ context.Context, tenantID, modelID string) (entity.InferenceModel, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.getModelErrors) > 0 {
		err := repository.getModelErrors[0]
		repository.getModelErrors = repository.getModelErrors[1:]
		if err != nil {
			return entity.InferenceModel{}, err
		}
	}
	if repository.getModelErr != nil {
		return entity.InferenceModel{}, repository.getModelErr
	}
	if repository.model.TenantID != tenantID || repository.model.ID != modelID {
		return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
	}
	return repository.model, nil
}

func (repository *fakeUpgradeRepository) ListRecoverableInferenceModelUpgrades(context.Context) ([]entity.InferenceModelUpgrade, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := make([]entity.InferenceModelUpgrade, 0)
	for _, attempt := range repository.attempts {
		if !attempt.State.Terminal() ||
			attempt.State == entity.InferenceModelUpgradeDegraded ||
			attempt.PendingCleanupContainerID != "" {
			result = append(result, attempt)
		}
	}
	return result, nil
}

func (repository *fakeUpgradeRepository) SaveInferenceModelUpgrade(_ context.Context, attempt entity.InferenceModelUpgrade) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.save != nil {
		if err := repository.save(attempt); err != nil {
			return err
		}
	}
	repository.attempts[attempt.ID] = attempt
	return nil
}

func (repository *fakeUpgradeRepository) ActivateInferenceModelUpgrade(
	_ context.Context,
	attempt entity.InferenceModelUpgrade,
	deployment entity.InferenceModelDeployment,
	supportedOutputModes []string,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	var activationErr error
	if len(repository.activateErrors) > 0 {
		activationErr = repository.activateErrors[0]
		repository.activateErrors = repository.activateErrors[1:]
	}
	if activationErr != nil && !repository.activateBeforeError {
		return activationErr
	}
	if supportedOutputModes != nil && !slices.Contains(supportedOutputModes, string(repository.model.OutputMode)) {
		return errors.New(apiError.InferenceUpgradeInvalid)
	}
	repository.attempts[attempt.ID] = attempt
	repository.model.ContainerID = deployment.ContainerID
	repository.model.DockerImage = deployment.ImageReference
	repository.model.Deployment = &deployment
	return activationErr
}

func (repository *fakeUpgradeRepository) ActivateAndRetargetInferenceModelUpgrade(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
	deployment entity.InferenceModelDeployment,
	supportedOutputModes []string,
	fromContainerID string,
	aliases []string,
) error {
	if err := repository.ActivateInferenceModelUpgrade(ctx, attempt, deployment, supportedOutputModes); err != nil {
		return err
	}
	if err := repository.RetargetInferenceIngestionJobs(
		ctx, attempt.TenantID, fromContainerID, deployment.ContainerID, deployment.ModelVersion,
	); err != nil {
		return err
	}
	for _, alias := range aliases {
		if alias == "" || alias == fromContainerID {
			continue
		}
		if err := repository.RetargetInferenceIngestionJobs(
			ctx, attempt.TenantID, alias, deployment.ContainerID, deployment.ModelVersion,
		); err != nil {
			return err
		}
	}
	return nil
}

func (repository *fakeUpgradeRepository) FinishInferenceModelUpgrade(
	_ context.Context,
	attempt entity.InferenceModelUpgrade,
	deployment *entity.InferenceModelDeployment,
	supportedOutputModes []string,
) error {
	if repository.finishStarted != nil {
		close(repository.finishStarted)
		repository.finishStarted = nil
		<-repository.finishRelease
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.finishCalls++
	var finishErr error
	if len(repository.finishErrors) > 0 {
		finishErr = repository.finishErrors[0]
		repository.finishErrors = repository.finishErrors[1:]
	}
	if finishErr != nil && !repository.finishBeforeError {
		return finishErr
	}
	if repository.model.ActiveUpgradeID != attempt.ID {
		return errors.New(apiError.DuplicateRecord)
	}
	if supportedOutputModes != nil && !slices.Contains(supportedOutputModes, string(repository.model.OutputMode)) {
		return errors.New(apiError.InferenceUpgradeInvalid)
	}
	repository.attempts[attempt.ID] = attempt
	if deployment != nil {
		repository.model.ContainerID = deployment.ContainerID
		repository.model.DockerImage = deployment.ImageReference
		repository.model.Deployment = deployment
	}
	repository.model.ActiveUpgradeID = ""
	return finishErr
}

func (repository *fakeUpgradeRepository) snapshot(id string) (entity.InferenceModelUpgrade, entity.InferenceModel) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.attempts[id], repository.model
}

type fakeUpgradeDocker struct {
	inspection      docker.InspectImageResult
	containers      map[string]docker.GetContainerInfoResult
	removed         []string
	pullErr         error
	pull            func(context.Context) error
	createErr       error
	createThenError bool
	createdFrom     string
	get             func(context.Context, string) (docker.GetContainerInfoResult, error)
	getErrors       map[string][]error
	removeErrs      map[string]error
}

func newFakeUpgradeDocker() *fakeUpgradeDocker {
	return &fakeUpgradeDocker{
		inspection: docker.InspectImageResult{
			ID: "sha256:local", RepoDigests: []string{"heartwisehub/model@" + testDigest},
			Labels: map[string]string{
				"org.opencontainers.image.version":  "2.0.0",
				"org.opencontainers.image.revision": testSourceSHA,
				"org.opencontainers.image.source":   "https://github.com/HeartWise-AI/pacs-ai-backend",
				"ai.heartwise.model.repository":     "HeartWise-AI/model",
				"ai.heartwise.model.revision":       testModelSHA,
				"ai.heartwise.model.weights.path":   "weights/model.pt",
				"ai.heartwise.model.weights.sha256": testWeightsSHA,
			},
		},
		containers: map[string]docker.GetContainerInfoResult{
			testOldContainer: {ID: testOldContainer, Name: "/active", Running: true},
		},
	}
}

func (client *fakeUpgradeDocker) PullImage(ctx context.Context, _ string) error {
	if client.pull != nil {
		return client.pull(ctx)
	}
	return client.pullErr
}
func (client *fakeUpgradeDocker) InspectImage(context.Context, string) (docker.InspectImageResult, error) {
	return client.inspection, nil
}
func (client *fakeUpgradeDocker) CreateContainer(_ context.Context, request docker.CreateContainer) (string, error) {
	client.createdFrom = request.Image
	if client.createErr != nil && !client.createThenError {
		return "", client.createErr
	}
	client.containers[testNewContainer] = docker.GetContainerInfoResult{ID: testNewContainer, Name: "/" + request.Name}
	return testNewContainer, client.createErr
}
func (client *fakeUpgradeDocker) GetContainerInfo(ctx context.Context, id string) (docker.GetContainerInfoResult, error) {
	if client.get != nil {
		return client.get(ctx, id)
	}
	if len(client.getErrors[id]) > 0 {
		err := client.getErrors[id][0]
		client.getErrors[id] = client.getErrors[id][1:]
		return docker.GetContainerInfoResult{}, err
	}
	container, exists := client.containers[id]
	if !exists {
		for _, candidate := range client.containers {
			if strings.TrimPrefix(candidate.Name, "/") == id {
				return candidate, nil
			}
		}
	}
	if !exists {
		return docker.GetContainerInfoResult{}, errors.New("missing container")
	}
	return container, nil
}
func (client *fakeUpgradeDocker) StartContainer(_ context.Context, id string) error {
	container := client.containers[id]
	container.Running = true
	client.containers[id] = container
	return nil
}
func (*fakeUpgradeDocker) StopContainer(context.Context, string) error { return nil }
func (client *fakeUpgradeDocker) RemoveContainer(_ context.Context, id string) error {
	if client.removeErrs != nil && client.removeErrs[id] != nil {
		return client.removeErrs[id]
	}
	client.removed = append(client.removed, id)
	delete(client.containers, id)
	return nil
}

type fakeUpgradeAPI struct {
	activeInfo    api.ModelInfo
	candidateInfo api.ModelInfo
	runtimeErr    error
}

func newFakeUpgradeAPI() *fakeUpgradeAPI {
	sourceRevision := testSourceSHA
	return &fakeUpgradeAPI{
		activeInfo: api.ModelInfo{
			ModelID: "runtime-model-1", Version: "1.0.0", SupportedOutputModes: []string{"JSON"},
		},
		candidateInfo: api.ModelInfo{
			ModelID: "runtime-model-1", Version: "2.0.0", SupportedOutputModes: []string{"JSON"},
			Resources: api.ModelResources{MaxConcurrentInferences: 1, IdleTimeoutSeconds: 60},
			Provenance: &api.ModelProvenance{
				SourceRepository: "HeartWise-AI/pacs-ai-backend", SourceRevision: &sourceRevision,
				ModelRepository: "HeartWise-AI/model", ModelRevision: testModelSHA,
				WeightsPath: "weights/model.pt", WeightsSHA256: testWeightsSHA,
			},
		},
	}
}

func (client *fakeUpgradeAPI) GetModelInfo(_ context.Context, name string) (api.GetModelInfoResponse, error) {
	if name == "active" {
		return api.GetModelInfoResponse{Success: true, Data: client.activeInfo}, nil
	}
	return api.GetModelInfoResponse{Success: true, Data: client.candidateInfo}, nil
}
func (client *fakeUpgradeAPI) GetModelRuntime(_ context.Context, name string) (api.ModelRuntime, error) {
	if client.runtimeErr != nil && name != "active" {
		return api.ModelRuntime{}, client.runtimeErr
	}
	return api.ModelRuntime{State: api.RuntimeReady, Loaded: true}, nil
}
func (*fakeUpgradeAPI) LoadModel(context.Context, string) (api.ModelRuntime, error) {
	return api.ModelRuntime{}, nil
}
func (*fakeUpgradeAPI) UnloadModel(context.Context, string) (api.ModelRuntime, error) {
	return api.ModelRuntime{}, nil
}

type fakeUpgradeManager struct {
	calls         []string
	block         func(context.Context, string) error
	prepare       func(context.Context, string) error
	blockErr      error
	blockErrors   map[string]error
	unblockErrors map[string]error
}

func (manager *fakeUpgradeManager) BlockAndDrain(ctx context.Context, id string) error {
	manager.calls = append(manager.calls, "block:"+id)
	if manager.block != nil {
		return manager.block(ctx, id)
	}
	if manager.blockErrors != nil && manager.blockErrors[id] != nil {
		return manager.blockErrors[id]
	}
	return manager.blockErr
}
func (manager *fakeUpgradeManager) ReplaceDrained(oldID, newID string) error {
	manager.calls = append(manager.calls, "replace:"+oldID+":"+newID)
	return nil
}
func (manager *fakeUpgradeManager) PrepareBlocked(ctx context.Context, id string) error {
	manager.calls = append(manager.calls, "prepare:"+id)
	if manager.prepare != nil {
		return manager.prepare(ctx, id)
	}
	return nil
}
func (manager *fakeUpgradeManager) Unblock(id string) error {
	manager.calls = append(manager.calls, "unblock:"+id)
	if manager.unblockErrors != nil && manager.unblockErrors[id] != nil {
		return manager.unblockErrors[id]
	}
	return nil
}

func testUpgradeService(repository *fakeUpgradeRepository, dockerClient *fakeUpgradeDocker, apiClient *fakeUpgradeAPI, manager *fakeUpgradeManager) *Service {
	return &Service{
		Repository: repository, Docker: dockerClient, API: apiClient, Manager: manager,
		Config: Config{
			AllowedNamespace: "heartwisehub", MaxDrainTimeout: time.Minute,
			OperationTimeout: time.Minute, ReadinessTimeout: time.Second, PollInterval: time.Millisecond,
		},
		Now:      func() time.Time { return time.Unix(100, 0) },
		NewID:    func() string { return "upgrade-1" },
		Launch:   func(run func()) { run() },
		Schedule: func(time.Duration, func()) {},
	}
}

func startUpgrade(t *testing.T, service *Service) entity.InferenceModelUpgrade {
	t.Helper()
	attempt, err := service.StartInferenceModelUpgrade(context.Background(), application.StartInferenceModelUpgrade{
		TenantID: "tenant-1", ModelID: "model-1", ActorUserID: "user-1",
		ImageReference: "heartwisehub/model:2.0.0", ExpectedDigest: testDigest,
		DrainTimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func TestUpgradeActivatesVerifiedCandidateAndPreservesAuditHistory(t *testing.T) {
	repository := newFakeUpgradeRepository()
	dockerClient := newFakeUpgradeDocker()
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeSucceeded || attempt.Candidate == nil {
		t.Fatalf("upgrade did not succeed: %+v", attempt)
	}
	if model.ContainerID != testNewContainer || model.ActiveUpgradeID != "" || model.Deployment.ImageDigest != testDigest {
		t.Fatalf("candidate registration was not committed: %+v", model)
	}
	if repository.jobContainerID != testNewContainer || repository.jobVersion != "2.0.0" {
		t.Fatalf("ingestion job target was not activated: container=%s version=%s", repository.jobContainerID, repository.jobVersion)
	}
	if !reflect.DeepEqual(repository.retargetCalls, []string{
		testOldContainer + "->" + testNewContainer,
		"active->" + testNewContainer,
	}) {
		t.Fatalf("container ID and stable name were not both redirected: %v", repository.retargetCalls)
	}
	if attempt.Previous.ContainerID != testOldContainer || attempt.Previous.ImageReference != "heartwisehub/model:1.0.0" {
		t.Fatalf("previous deployment was not retained in history: %+v", attempt.Previous)
	}
	expectedCalls := []string{
		"block:" + testOldContainer,
		"replace:" + testOldContainer + ":" + testNewContainer,
		"prepare:" + testNewContainer,
		"unblock:" + testNewContainer,
	}
	if !reflect.DeepEqual(manager.calls, expectedCalls) {
		t.Fatalf("unexpected manager sequence: %v", manager.calls)
	}
	if !reflect.DeepEqual(dockerClient.removed, []string{testOldContainer}) {
		t.Fatalf("old container was not cleaned up after commit: %v", dockerClient.removed)
	}
	if dockerClient.createdFrom != dockerClient.inspection.ID {
		t.Fatalf("candidate was not created from the inspected immutable image: %s", dockerClient.createdFrom)
	}
	auditJSON, err := json.Marshal(attempt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(auditJSON), "TOKEN=secret") {
		t.Fatal("model environment secret leaked into the upgrade audit record")
	}
}

func TestUpgradeResumesAfterCommittedBeginResponseIsLost(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.beginErr = errors.New(apiError.FirestoreError)
	repository.beginBeforeError = true
	repository.getAttemptErrors = []error{errors.New(apiError.FirestoreError)}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), &fakeUpgradeManager{})

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeSucceeded || model.ActiveUpgradeID != "" || model.ContainerID != testNewContainer {
		t.Fatalf("ambiguous committed begin was not resumed: attempt=%+v model=%+v", attempt, model)
	}
}

func TestInvalidRegistrationFinalizationRetriesBeforeReturning(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ContainerID = ""
	repository.finishErrors = []error{errors.New(apiError.FirestoreError)}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), &fakeUpgradeManager{})

	_, err := service.StartInferenceModelUpgrade(context.Background(), application.StartInferenceModelUpgrade{
		TenantID: "tenant-1", ModelID: "model-1", ActorUserID: "user-1",
		ImageReference: "heartwisehub/model:2.0.0", ExpectedDigest: testDigest,
		DrainTimeoutSeconds: 10,
	})
	if err == nil {
		t.Fatal("invalid registration was accepted")
	}
	attempt, model := repository.snapshot("upgrade-1")
	if repository.finishCalls != 2 || attempt.State != entity.InferenceModelUpgradeFailed || model.ActiveUpgradeID != "" {
		t.Fatalf("invalid registration finalization was not confirmed: calls=%d attempt=%+v model=%+v",
			repository.finishCalls, attempt, model,
		)
	}
}

func TestSuccessfulUpgradeWaitsForAcceptedProcessingJobsBeforeCleanup(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.activeJobs = []bool{true, true, false}
	dockerClient := newFakeUpgradeDocker()
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})

	started := startUpgrade(t, service)
	attempt, _ := repository.snapshot(started.ID)
	if attempt.PendingCleanupContainerID != "" {
		t.Fatalf("old container cleanup remained pending: %+v", attempt)
	}
	if repository.activeJobCalls != 3 {
		t.Fatalf("cleanup did not wait for active processing jobs: calls=%d", repository.activeJobCalls)
	}
	if repository.activeJobTenant != "tenant-1" || repository.activeJobModel != "Model" || repository.activeJobVersion != "1.0.0" {
		t.Fatalf("cleanup queried the wrong deployment: tenant=%s model=%s version=%s",
			repository.activeJobTenant, repository.activeJobModel, repository.activeJobVersion,
		)
	}
	if !reflect.DeepEqual(dockerClient.removed, []string{testOldContainer}) {
		t.Fatalf("old container was not removed after processing completed: %v", dockerClient.removed)
	}
}

func TestSuccessfulUpgradeSchedulesRetryWhenCleanupCommitFails(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.save = func(attempt entity.InferenceModelUpgrade) error {
		if attempt.State == entity.InferenceModelUpgradeSucceeded && attempt.PendingCleanupContainerID == "" {
			return errors.New(apiError.FirestoreError)
		}
		return nil
	}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), &fakeUpgradeManager{})
	scheduled := 0
	service.Schedule = func(time.Duration, func()) { scheduled++ }

	started := startUpgrade(t, service)
	attempt, _ := repository.snapshot(started.ID)
	if scheduled != 1 || attempt.PendingCleanupContainerID != testOldContainer {
		t.Fatalf("cleanup persistence failure was not left durable and retried: scheduled=%d attempt=%+v", scheduled, attempt)
	}
}

func TestSuccessfulUpgradeRetainsOldContainerWhenProcessingStateIsUnavailable(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.activeJobsErr = errors.New("processing state unavailable")
	dockerClient := newFakeUpgradeDocker()
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})

	started := startUpgrade(t, service)
	attempt, _ := repository.snapshot(started.ID)
	if attempt.PendingCleanupContainerID != testOldContainer {
		t.Fatalf("cleanup identity was not retained: %+v", attempt)
	}
	if slices.Contains(dockerClient.removed, testOldContainer) {
		t.Fatalf("old container was removed without a reliable processing-state check: %v", dockerClient.removed)
	}
}

func TestPendingCleanupRetriesInProcessAfterTransientFailure(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.activeJobsErr = errors.New("processing state unavailable")
	dockerClient := newFakeUpgradeDocker()
	scheduled := make(chan func(), 1)
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})
	service.Schedule = func(_ time.Duration, run func()) { scheduled <- run }

	started := startUpgrade(t, service)
	attempt, _ := repository.snapshot(started.ID)
	if attempt.PendingCleanupContainerID != testOldContainer {
		t.Fatalf("cleanup was not retained after the transient failure: %+v", attempt)
	}
	repository.activeJobsErr = nil
	select {
	case retry := <-scheduled:
		retry()
	default:
		t.Fatal("pending cleanup retry was not scheduled")
	}
	attempt, _ = repository.snapshot(started.ID)
	if attempt.PendingCleanupContainerID != "" || !slices.Contains(dockerClient.removed, testOldContainer) {
		t.Fatalf("scheduled cleanup did not complete: attempt=%+v removed=%v", attempt, dockerClient.removed)
	}
}

func TestUncertainSuccessSchedulesDeduplicatedInProcessRecovery(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ContainerID = testNewContainer
	repository.model.ActiveUpgradeID = "upgrade-1"
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1", ModelName: "Model",
		State: entity.InferenceModelUpgradeVerifying,
		Previous: entity.InferenceModelDeployment{
			ContainerID: testOldContainer, ModelVersion: "1.0.0",
		},
		Candidate: &entity.InferenceModelDeployment{
			ContainerID: testNewContainer, ModelVersion: "2.0.0",
		},
	}
	dockerClient := newFakeUpgradeDocker()
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})
	scheduled := make(chan func(), 1)
	service.Schedule = func(_ time.Duration, run func()) { scheduled <- run }
	if err := service.defaults(); err != nil {
		t.Fatal(err)
	}

	service.scheduleRecovery(repository.attempts["upgrade-1"])
	service.scheduleRecovery(repository.attempts["upgrade-1"])
	repository.mu.Lock()
	committed := repository.attempts["upgrade-1"]
	committed.State = entity.InferenceModelUpgradeSucceeded
	committed.PendingCleanupContainerID = testOldContainer
	repository.attempts["upgrade-1"] = committed
	repository.model.ActiveUpgradeID = ""
	repository.mu.Unlock()
	select {
	case retry := <-scheduled:
		retry()
	default:
		t.Fatal("uncertain success recovery was not scheduled")
	}
	select {
	case <-scheduled:
		t.Fatal("uncertain success recovery was scheduled more than once")
	default:
	}

	attempt, _ := repository.snapshot("upgrade-1")
	if attempt.PendingCleanupContainerID != "" || !slices.Contains(dockerClient.removed, testOldContainer) {
		t.Fatalf("scheduled success recovery did not finish cleanup: attempt=%+v removed=%v", attempt, dockerClient.removed)
	}
}

func TestSuccessFinalizationConfirmsCommitAfterLostResponse(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.finishErrors = []error{errors.New("success response lost")}
	repository.finishBeforeError = true
	repository.getModelErrors = []error{errors.New("confirmation read unavailable")}
	dockerClient := newFakeUpgradeDocker()
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if repository.finishCalls != 2 {
		t.Fatalf("ambiguous success finalization was not retried: calls=%d", repository.finishCalls)
	}
	if attempt.State != entity.InferenceModelUpgradeSucceeded || model.ActiveUpgradeID != "" || model.ContainerID != testNewContainer {
		t.Fatalf("committed success was not confirmed safely: attempt=%+v model=%+v", attempt, model)
	}
	if !reflect.DeepEqual(dockerClient.removed, []string{testOldContainer}) {
		t.Fatalf("old container was not cleaned up after success confirmation: %v", dockerClient.removed)
	}
}

func TestSuccessFinalizationRevalidatesCurrentOutputModeBeforeClearingLock(t *testing.T) {
	repository := newFakeUpgradeRepository()
	finishStarted := make(chan struct{})
	finishRelease := make(chan struct{})
	repository.finishStarted = finishStarted
	repository.finishRelease = finishRelease
	dockerClient := newFakeUpgradeDocker()
	apiClient := newFakeUpgradeAPI()
	apiClient.activeInfo.SupportedOutputModes = []string{"JSON", "HTML"}
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, dockerClient, apiClient, manager)
	done := make(chan struct{})
	service.Launch = func(run func()) {
		go func() {
			defer close(done)
			run()
		}()
	}

	started := startUpgrade(t, service)
	<-finishStarted
	repository.mu.Lock()
	repository.model.OutputMode = entity.OutputModeHTML
	repository.mu.Unlock()
	close(finishRelease)
	<-done

	attempt, model := repository.snapshot(started.ID)
	if model.ActiveUpgradeID != "" || model.ContainerID != testOldContainer || model.OutputMode != entity.OutputModeHTML {
		t.Fatalf("incompatible candidate was not rolled back to the compatible previous deployment: %+v", model)
	}
	if attempt.State != entity.InferenceModelUpgradeRolledBack {
		t.Fatalf("incompatible success did not finish as rolled back: %+v", attempt)
	}
	if !reflect.DeepEqual(dockerClient.removed, []string{testNewContainer}) {
		t.Fatalf("candidate was not cleaned up after safe rollback: %v", dockerClient.removed)
	}
}

func TestUpgradeRollsBackRegistrationAndManagerAfterActivationFailure(t *testing.T) {
	repository := newFakeUpgradeRepository()
	dockerClient := newFakeUpgradeDocker()
	apiClient := newFakeUpgradeAPI()
	apiClient.runtimeErr = errors.New("runtime unavailable")
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, dockerClient, apiClient, manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeRolledBack {
		t.Fatalf("upgrade was not rolled back: %+v", attempt)
	}
	if model.ContainerID != testOldContainer || model.DockerImage != "heartwisehub/model:1.0.0" || model.ActiveUpgradeID != "" {
		t.Fatalf("previous registration was not restored: %+v", model)
	}
	if repository.jobContainerID != testOldContainer || repository.jobVersion != "1.0.0" {
		t.Fatalf("ingestion job target was not restored: container=%s version=%s", repository.jobContainerID, repository.jobVersion)
	}
	expectedSuffix := []string{
		"block:" + testNewContainer,
		"replace:" + testNewContainer + ":" + testOldContainer,
		"prepare:" + testOldContainer,
		"unblock:" + testOldContainer,
	}
	if !reflect.DeepEqual(manager.calls[len(manager.calls)-len(expectedSuffix):], expectedSuffix) {
		t.Fatalf("manager rollback sequence was incomplete: %v", manager.calls)
	}
	if !reflect.DeepEqual(dockerClient.removed, []string{testNewContainer}) {
		t.Fatalf("failed candidate was not removed: %v", dockerClient.removed)
	}
}

func TestActivationRevalidatesCurrentRegisteredOutputModeTransactionally(t *testing.T) {
	repository := newFakeUpgradeRepository()
	manager := &fakeUpgradeManager{}
	manager.prepare = func(_ context.Context, id string) error {
		if id == testNewContainer {
			repository.mu.Lock()
			repository.model.OutputMode = entity.OutputModeHTML
			repository.mu.Unlock()
		}
		return nil
	}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeFailed {
		t.Fatalf("incompatible current output mode did not fail activation: %+v", attempt)
	}
	if model.ContainerID != testOldContainer || model.OutputMode != entity.OutputModeHTML || model.ActiveUpgradeID != "" {
		t.Fatalf("failed activation did not preserve the current registration: %+v", model)
	}
}

func TestRollbackContinuesAfterCommittedRegistrationResponseIsLost(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.activateErrors = []error{nil, errors.New("rollback response lost")}
	repository.activateBeforeError = true
	dockerClient := newFakeUpgradeDocker()
	apiClient := newFakeUpgradeAPI()
	apiClient.runtimeErr = errors.New("runtime unavailable")
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, dockerClient, apiClient, manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeRolledBack || model.ActiveUpgradeID != "" || model.ContainerID != testOldContainer {
		t.Fatalf("ambiguous rollback commit was not completed: attempt=%+v model=%+v", attempt, model)
	}
	if repository.jobContainerID != testOldContainer || repository.jobVersion != "1.0.0" {
		t.Fatalf("ingestion job target was not restored: container=%s version=%s", repository.jobContainerID, repository.jobVersion)
	}
	if manager.calls[len(manager.calls)-1] != "unblock:"+testOldContainer {
		t.Fatalf("previous deployment admission was not reopened: %v", manager.calls)
	}
}

func TestRollbackRevalidatesCurrentOutputModeAgainstPreviousDeployment(t *testing.T) {
	repository := newFakeUpgradeRepository()
	dockerClient := newFakeUpgradeDocker()
	apiClient := newFakeUpgradeAPI()
	apiClient.candidateInfo.SupportedOutputModes = []string{"JSON", "HTML"}
	apiClient.runtimeErr = errors.New("runtime unavailable")
	manager := &fakeUpgradeManager{}
	manager.prepare = func(_ context.Context, id string) error {
		if id == testOldContainer {
			repository.mu.Lock()
			repository.model.OutputMode = entity.OutputModeHTML
			repository.mu.Unlock()
		}
		return nil
	}
	service := testUpgradeService(repository, dockerClient, apiClient, manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeDegraded || model.ContainerID != testNewContainer {
		t.Fatalf("incompatible rollback did not fail closed: attempt=%+v model=%+v", attempt, model)
	}
	if model.OutputMode != entity.OutputModeHTML || model.ActiveUpgradeID == "" {
		t.Fatalf("incompatible rollback changed or unlocked the registration: %+v", model)
	}
	if slices.Contains(manager.calls, "unblock:"+testOldContainer) {
		t.Fatalf("incompatible previous deployment was unblocked: %v", manager.calls)
	}
}

func TestUpgradeKeepsBothDeploymentsWhenActivationOutcomeCannotBeRead(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.activateErrors = []error{errors.New("activation response lost")}
	repository.activateBeforeError = true
	repository.getModelErr = errors.New("firestore unavailable")
	dockerClient := newFakeUpgradeDocker()
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeDegraded || attempt.FailureStage != entity.InferenceModelUpgradeVerifying {
		t.Fatalf("uncertain activation was not locked as degraded: %+v", attempt)
	}
	if model.ContainerID != testNewContainer || model.ActiveUpgradeID != started.ID {
		t.Fatalf("uncertain registration was modified: %+v", model)
	}
	if _, exists := dockerClient.containers[testOldContainer]; !exists {
		t.Fatal("previous container was removed after an uncertain activation")
	}
	if _, exists := dockerClient.containers[testNewContainer]; !exists {
		t.Fatal("candidate container was removed after an uncertain activation")
	}
	if len(dockerClient.removed) != 0 {
		t.Fatalf("a container was removed after an uncertain activation: %v", dockerClient.removed)
	}
}

func TestRollbackFinalizationRetriesWithoutRequiringRestart(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.finishErrors = []error{errors.New("transient firestore failure")}
	dockerClient := newFakeUpgradeDocker()
	apiClient := newFakeUpgradeAPI()
	apiClient.runtimeErr = errors.New("runtime unavailable")
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, dockerClient, apiClient, manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if repository.finishCalls != 2 {
		t.Fatalf("rollback finalization was not retried: calls=%d", repository.finishCalls)
	}
	if attempt.State != entity.InferenceModelUpgradeRolledBack || model.ActiveUpgradeID != "" || model.ContainerID != testOldContainer {
		t.Fatalf("rollback retry did not finalize safely: attempt=%+v model=%+v", attempt, model)
	}
}

func TestFailedTerminalFinalizationSchedulesInProcessRecovery(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ActiveUpgradeID = "upgrade-1"
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State:    entity.InferenceModelUpgradeValidating,
		Previous: entity.InferenceModelDeployment{ContainerID: testOldContainer},
	}
	for index := 0; index < 100; index++ {
		repository.finishErrors = append(repository.finishErrors, errors.New(apiError.FirestoreError))
	}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), &fakeUpgradeManager{})
	scheduled := make(chan func(), 1)
	service.Schedule = func(_ time.Duration, run func()) { scheduled <- run }
	if err := service.defaults(); err != nil {
		t.Fatal(err)
	}
	service.Config.OperationTimeout = 5 * time.Millisecond
	operation := &upgradeOperation{
		attempt:   repository.attempts["upgrade-1"],
		model:     repository.model,
		startedAt: time.Now(),
	}

	service.fail(context.Background(), operation, errors.New("candidate validation failed"))

	select {
	case <-scheduled:
	case <-time.After(time.Second):
		t.Fatal("failed terminal finalization did not schedule in-process recovery")
	}
	attempt, model := repository.snapshot("upgrade-1")
	if attempt.State.Terminal() || model.ActiveUpgradeID != "upgrade-1" {
		t.Fatalf("unconfirmed failure finalization unexpectedly cleared durable recovery state: attempt=%+v model=%+v", attempt, model)
	}
}

func TestRollbackFinalizationRevalidatesCurrentOutputModeBeforeClearingLock(t *testing.T) {
	repository := newFakeUpgradeRepository()
	finishStarted := make(chan struct{})
	finishRelease := make(chan struct{})
	repository.finishStarted = finishStarted
	repository.finishRelease = finishRelease
	dockerClient := newFakeUpgradeDocker()
	apiClient := newFakeUpgradeAPI()
	apiClient.runtimeErr = errors.New("runtime unavailable")
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, dockerClient, apiClient, manager)
	done := make(chan struct{})
	service.Launch = func(run func()) {
		go func() {
			defer close(done)
			run()
		}()
	}

	started := startUpgrade(t, service)
	<-finishStarted
	repository.mu.Lock()
	repository.model.OutputMode = entity.OutputModeHTML
	repository.mu.Unlock()
	close(finishRelease)
	<-done

	attempt, model := repository.snapshot(started.ID)
	if model.ActiveUpgradeID != started.ID || model.ContainerID != testOldContainer || model.OutputMode != entity.OutputModeHTML {
		t.Fatalf("incompatible rollback finalization cleared or rewrote the registration: %+v", model)
	}
	if attempt.State != entity.InferenceModelUpgradeDegraded {
		t.Fatalf("incompatible rollback finalization was not degraded: %+v", attempt)
	}
	if len(dockerClient.removed) != 0 {
		t.Fatalf("candidate cleanup ran after incompatible rollback finalization: %v", dockerClient.removed)
	}
	blockPreviousCalls := 0
	for _, call := range manager.calls {
		if call == "block:"+testOldContainer {
			blockPreviousCalls++
		}
	}
	if blockPreviousCalls < 2 {
		t.Fatalf("incompatible restored deployment was not blocked again: %v", manager.calls)
	}
}

func TestUpgradeDrainFailureLeavesPreviousRegistrationActive(t *testing.T) {
	repository := newFakeUpgradeRepository()
	dockerClient := newFakeUpgradeDocker()
	manager := &fakeUpgradeManager{blockErr: context.DeadlineExceeded}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeFailed {
		t.Fatalf("upgrade did not fail before activation: %+v", attempt)
	}
	if model.ContainerID != testOldContainer || model.ActiveUpgradeID != "" {
		t.Fatalf("active registration changed after drain failure: %+v", model)
	}
	if !reflect.DeepEqual(manager.calls, []string{"block:" + testOldContainer, "unblock:" + testOldContainer}) {
		t.Fatalf("admission was not restored: %v", manager.calls)
	}
}

func TestCreateResponseLossDiscoversAndRemovesCandidateByDeterministicName(t *testing.T) {
	repository := newFakeUpgradeRepository()
	dockerClient := newFakeUpgradeDocker()
	dockerClient.createErr = errors.New("container create response lost")
	dockerClient.createThenError = true
	dockerClient.getErrors = map[string][]error{
		candidateContainerName("upgrade-1"): {errors.New("transient Docker inspection failure")},
	}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeFailed || attempt.Candidate == nil || attempt.Candidate.ContainerID != testNewContainer {
		t.Fatalf("ambiguous container creation was not recorded safely: %+v", attempt)
	}
	if model.ContainerID != testOldContainer || model.ActiveUpgradeID != "" {
		t.Fatalf("ambiguous container creation changed the registration: %+v", model)
	}
	if !reflect.DeepEqual(dockerClient.removed, []string{testNewContainer}) {
		t.Fatalf("discovered candidate was not removed: %v", dockerClient.removed)
	}
}

func TestInconclusiveCreateDiscoveryPersistsDeterministicCleanupName(t *testing.T) {
	repository := newFakeUpgradeRepository()
	dockerClient := newFakeUpgradeDocker()
	dockerClient.createErr = errors.New("Docker unavailable during create")
	name := candidateContainerName("upgrade-1")
	dockerClient.removeErrs = map[string]error{name: errors.New("Docker still unavailable")}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})
	service.Config.ReadinessTimeout = 2 * time.Millisecond

	started := startUpgrade(t, service)
	attempt, _ := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeFailed || attempt.Candidate == nil ||
		attempt.Candidate.ContainerID != name || attempt.PendingCleanupContainerID != name {
		t.Fatalf("inconclusive creation did not retain deterministic cleanup work: %+v", attempt)
	}
}

func TestStartRejectsConcurrentUpgradeWithConflict(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.beginErr = errors.New(apiError.DuplicateRecord)
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), &fakeUpgradeManager{})
	_, err := service.StartInferenceModelUpgrade(context.Background(), application.StartInferenceModelUpgrade{
		TenantID: "tenant-1", ModelID: "model-1", ActorUserID: "user-1",
		ImageReference: "heartwisehub/model:2.0.0", DrainTimeoutSeconds: 10,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate upgrade did not return a conflict: %v", err)
	}
}

func TestStartRejectsSharedContainerBeforeLaunchingUpgrade(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.beginErr = errors.New(apiError.InferenceUpgradeConflict)
	dockerClient := newFakeUpgradeDocker()
	manager := &fakeUpgradeManager{}
	launched := false
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), manager)
	service.Launch = func(func()) { launched = true }

	_, err := service.StartInferenceModelUpgrade(context.Background(), application.StartInferenceModelUpgrade{
		TenantID: "tenant-1", ModelID: "model-1", ActorUserID: "user-1",
		ImageReference: "heartwisehub/model:2.0.0", DrainTimeoutSeconds: 10,
	})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 409 || httpErr.Code != apiError.InferenceUpgradeConflict {
		t.Fatalf("shared container did not return a typed conflict: %v", err)
	}
	if !strings.Contains(err.Error(), "shares its container") {
		t.Fatalf("shared-container conflict was not actionable: %v", err)
	}
	if launched || len(manager.calls) != 0 || len(dockerClient.removed) != 0 {
		t.Fatalf("shared container caused side effects: launched=%v manager=%v removed=%v",
			launched, manager.calls, dockerClient.removed,
		)
	}
}

func TestUpgradeRechecksSharedContainerBeforeDrain(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.sharedContainers = map[string]bool{testOldContainer: true}
	dockerClient := newFakeUpgradeDocker()
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeFailed || model.ContainerID != testOldContainer {
		t.Fatalf("shared container was not failed safely: attempt=%+v model=%+v", attempt, model)
	}
	if len(manager.calls) != 0 {
		t.Fatalf("shared container reached model-manager drain: %v", manager.calls)
	}
	if !reflect.DeepEqual(dockerClient.removed, []string{testNewContainer}) {
		t.Fatalf("candidate cleanup was not isolated from the shared deployment: %v", dockerClient.removed)
	}
}

func TestCleanupRefusesRegisteredContainer(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.sharedContainers = map[string]bool{testOldContainer: true}
	dockerClient := newFakeUpgradeDocker()
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})

	err := service.removeUnregisteredContainer(context.Background(), testOldContainer)
	if err == nil || !strings.Contains(err.Error(), "still referenced") {
		t.Fatalf("registered container was accepted for cleanup: %v", err)
	}
	if len(dockerClient.removed) != 0 {
		t.Fatalf("registered container was removed: %v", dockerClient.removed)
	}
}

func TestStartRejectsModelClaimedForDeletion(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.DeletionClaimID = "deletion-1"
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), &fakeUpgradeManager{})
	_, err := service.StartInferenceModelUpgrade(context.Background(), application.StartInferenceModelUpgrade{
		TenantID: "tenant-1", ModelID: "model-1", ActorUserID: "user-1",
		ImageReference: "heartwisehub/model:2.0.0", DrainTimeoutSeconds: 10,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("deletion claim did not exclude a new upgrade: %v", err)
	}
}

func TestDefaultDrainTimeoutIsBoundedByConfiguredMaximum(t *testing.T) {
	repository := newFakeUpgradeRepository()
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), &fakeUpgradeManager{})
	service.Config.MaxDrainTimeout = 60 * time.Second
	attempt, err := service.StartInferenceModelUpgrade(context.Background(), application.StartInferenceModelUpgrade{
		TenantID: "tenant-1", ModelID: "model-1", ActorUserID: "user-1",
		ImageReference: "heartwisehub/model:2.0.0", ExpectedDigest: testDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.DrainTimeoutSeconds != 60 {
		t.Fatalf("default drain timeout exceeded the configured maximum: %d", attempt.DrainTimeoutSeconds)
	}
}

func TestRecoverRollsBackInterruptedActivatedUpgrade(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ContainerID = testNewContainer
	repository.model.DockerImage = "heartwisehub/model:2.0.0"
	repository.model.ActiveUpgradeID = "upgrade-1"
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State:     entity.InferenceModelUpgradeVerifying,
		Previous:  entity.InferenceModelDeployment{ContainerID: testOldContainer, ImageReference: "heartwisehub/model:1.0.0"},
		Candidate: &entity.InferenceModelDeployment{ContainerID: testNewContainer, ImageReference: "heartwisehub/model:2.0.0"},
	}
	dockerClient := newFakeUpgradeDocker()
	dockerClient.containers[testNewContainer] = docker.GetContainerInfoResult{ID: testNewContainer, Name: "/candidate", Running: true}
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), manager)

	if err := service.RecoverInferenceModelUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt, model := repository.snapshot("upgrade-1")
	if attempt.State != entity.InferenceModelUpgradeRolledBack || model.ContainerID != testOldContainer || model.ActiveUpgradeID != "" {
		t.Fatalf("restart recovery did not restore the previous deployment: attempt=%+v model=%+v", attempt, model)
	}
}

func TestRecoveryIsAStartupBarrierBeforeAdmissionOpens(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ContainerID = testNewContainer
	repository.model.DockerImage = "heartwisehub/model:2.0.0"
	repository.model.ActiveUpgradeID = "upgrade-1"
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State: entity.InferenceModelUpgradeVerifying,
		Previous: entity.InferenceModelDeployment{
			ContainerID: testOldContainer, ImageReference: "heartwisehub/model:1.0.0", ModelVersion: "1.0.0",
		},
		Candidate: &entity.InferenceModelDeployment{
			ContainerID: testNewContainer, ImageReference: "heartwisehub/model:2.0.0", ModelVersion: "2.0.0",
		},
	}
	dockerClient := newFakeUpgradeDocker()
	dockerClient.containers[testNewContainer] = docker.GetContainerInfoResult{ID: testNewContainer, Name: "/candidate", Running: true}
	blockStarted := make(chan struct{})
	releaseBlock := make(chan struct{})
	manager := &fakeUpgradeManager{block: func(context.Context, string) error {
		close(blockStarted)
		<-releaseBlock
		return nil
	}}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), manager)
	service.Launch = func(run func()) { go run() }
	recovered := make(chan error, 1)
	go func() { recovered <- service.RecoverInferenceModelUpgrades(context.Background()) }()

	<-blockStarted
	select {
	case err := <-recovered:
		t.Fatalf("startup recovery returned before reconciliation completed: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(releaseBlock)
	if err := <-recovered; err != nil {
		t.Fatal(err)
	}
	attempt, model := repository.snapshot("upgrade-1")
	if attempt.State != entity.InferenceModelUpgradeRolledBack || model.ContainerID != testOldContainer || model.ActiveUpgradeID != "" {
		t.Fatalf("startup barrier did not finish recovery: attempt=%+v model=%+v", attempt, model)
	}
}

func TestRecoveryKeepsLockBearingDegradedDeploymentBlocked(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ContainerID = testNewContainer
	repository.model.ActiveUpgradeID = "upgrade-1"
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State:     entity.InferenceModelUpgradeDegraded,
		Previous:  entity.InferenceModelDeployment{ContainerID: testOldContainer},
		Candidate: &entity.InferenceModelDeployment{ContainerID: testNewContainer},
	}
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), manager)

	if err := service.RecoverInferenceModelUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt, model := repository.snapshot("upgrade-1")
	if attempt.State != entity.InferenceModelUpgradeDegraded || model.ActiveUpgradeID != "upgrade-1" {
		t.Fatalf("degraded lock was modified during startup recovery: attempt=%+v model=%+v", attempt, model)
	}
	if !reflect.DeepEqual(manager.calls, []string{"block:" + testNewContainer}) {
		t.Fatalf("degraded registered deployment was not blocked: %v", manager.calls)
	}
}

func TestRecoveryBlocksUnknownRegisteredDeploymentBeforeDegrading(t *testing.T) {
	unknownContainer := strings.Repeat("c", 64)
	repository := newFakeUpgradeRepository()
	repository.model.ContainerID = unknownContainer
	repository.model.ActiveUpgradeID = "upgrade-1"
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State:     entity.InferenceModelUpgradeVerifying,
		Previous:  entity.InferenceModelDeployment{ContainerID: testOldContainer},
		Candidate: &entity.InferenceModelDeployment{ContainerID: testNewContainer},
	}
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), manager)

	if err := service.RecoverInferenceModelUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt, model := repository.snapshot("upgrade-1")
	if attempt.State != entity.InferenceModelUpgradeDegraded || model.ActiveUpgradeID != "upgrade-1" {
		t.Fatalf("unknown deployment was not retained under degraded lock: attempt=%+v model=%+v", attempt, model)
	}
	if !reflect.DeepEqual(manager.calls, []string{"block:" + unknownContainer}) {
		t.Fatalf("unknown registered deployment was not blocked: %v", manager.calls)
	}
}

func TestCancelBeforeActivationTerminatesSafely(t *testing.T) {
	repository := newFakeUpgradeRepository()
	dockerClient := newFakeUpgradeDocker()
	pullStarted := make(chan struct{})
	dockerClient.pull = func(ctx context.Context) error {
		close(pullStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})
	service.Launch = func(run func()) { go run() }

	started := startUpgrade(t, service)
	<-pullStarted
	if _, err := service.CancelInferenceModelUpgrade(context.Background(), "tenant-1", "model-1", started.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		attempt, model := repository.snapshot(started.ID)
		if attempt.State == entity.InferenceModelUpgradeCancelled {
			if model.ContainerID != testOldContainer || model.ActiveUpgradeID != "" {
				t.Fatalf("cancellation changed the active deployment: %+v", model)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancellation did not complete: %+v", attempt)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCancellationDuringActiveReadinessIsRecordedAsCancelled(t *testing.T) {
	repository := newFakeUpgradeRepository()
	dockerClient := newFakeUpgradeDocker()
	readinessStarted := make(chan struct{})
	dockerClient.get = func(ctx context.Context, id string) (docker.GetContainerInfoResult, error) {
		if id != testOldContainer {
			return docker.GetContainerInfoResult{}, errors.New("unexpected container")
		}
		close(readinessStarted)
		<-ctx.Done()
		return docker.GetContainerInfoResult{}, ctx.Err()
	}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})
	service.Launch = func(run func()) { go run() }

	started := startUpgrade(t, service)
	<-readinessStarted
	if _, err := service.CancelInferenceModelUpgrade(context.Background(), "tenant-1", "model-1", started.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		attempt, _ := repository.snapshot(started.ID)
		if attempt.State == entity.InferenceModelUpgradeCancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("readiness cancellation was not preserved: %+v", attempt)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCancelIsRejectedAfterSuccessCommitPhaseBegins(t *testing.T) {
	repository := newFakeUpgradeRepository()
	finishStarted := make(chan struct{})
	finishRelease := make(chan struct{})
	repository.finishStarted = finishStarted
	repository.finishRelease = finishRelease
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), &fakeUpgradeManager{})
	service.Launch = func(run func()) { go run() }

	started := startUpgrade(t, service)
	<-finishStarted
	if _, err := service.CancelInferenceModelUpgrade(context.Background(), "tenant-1", "model-1", started.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancellation was accepted after commit began: %v", err)
	}
	close(finishRelease)
	deadline := time.Now().Add(time.Second)
	for {
		attempt, _ := repository.snapshot(started.ID)
		if attempt.State == entity.InferenceModelUpgradeSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("success finalization did not complete: %+v", attempt)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCancelIsRejectedAfterFailureRollbackBegins(t *testing.T) {
	repository := newFakeUpgradeRepository()
	apiClient := newFakeUpgradeAPI()
	apiClient.runtimeErr = errors.New("candidate verification failed")
	rollbackStarted := make(chan struct{})
	rollbackRelease := make(chan struct{})
	manager := &fakeUpgradeManager{block: func(ctx context.Context, id string) error {
		if id != testNewContainer {
			return nil
		}
		close(rollbackStarted)
		select {
		case <-rollbackRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), apiClient, manager)
	service.Launch = func(run func()) { go run() }

	started := startUpgrade(t, service)
	<-rollbackStarted
	if _, err := service.CancelInferenceModelUpgrade(
		context.Background(), "tenant-1", "model-1", started.ID,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancellation was accepted after failure rollback began: %v", err)
	}
	close(rollbackRelease)
	deadline := time.Now().Add(time.Second)
	for {
		attempt, _ := repository.snapshot(started.ID)
		if attempt.State == entity.InferenceModelUpgradeRolledBack {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failure rollback did not complete: %+v", attempt)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCancelRejectsAlreadyCompletedUpgrade(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State: entity.InferenceModelUpgradeSucceeded,
	}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), &fakeUpgradeManager{})

	if _, err := service.CancelInferenceModelUpgrade(
		context.Background(), "tenant-1", "model-1", "upgrade-1",
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("completed upgrade cancellation did not return conflict: %v", err)
	}
}

func TestRollbackFailureIsDurablyDegradedAndKeepsUpgradeLock(t *testing.T) {
	repository := newFakeUpgradeRepository()
	dockerClient := newFakeUpgradeDocker()
	apiClient := newFakeUpgradeAPI()
	apiClient.runtimeErr = errors.New("runtime unavailable")
	manager := &fakeUpgradeManager{blockErrors: map[string]error{testNewContainer: errors.New("candidate cannot drain")}}
	service := testUpgradeService(repository, dockerClient, apiClient, manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeDegraded || attempt.RollbackError == "" {
		t.Fatalf("rollback failure was not marked degraded: %+v", attempt)
	}
	if model.ActiveUpgradeID != started.ID {
		t.Fatalf("degraded registration lock was cleared: %+v", model)
	}
}

func TestDegradedStatePersistenceRetriesTransientFailure(t *testing.T) {
	repository := newFakeUpgradeRepository()
	degradedSaveCalls := 0
	repository.save = func(attempt entity.InferenceModelUpgrade) error {
		if attempt.State == entity.InferenceModelUpgradeDegraded {
			degradedSaveCalls++
			if degradedSaveCalls == 1 {
				return errors.New(apiError.FirestoreError)
			}
		}
		return nil
	}
	apiClient := newFakeUpgradeAPI()
	apiClient.runtimeErr = errors.New("runtime unavailable")
	manager := &fakeUpgradeManager{blockErrors: map[string]error{testNewContainer: errors.New("candidate cannot drain")}}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), apiClient, manager)

	started := startUpgrade(t, service)
	attempt, _ := repository.snapshot(started.ID)
	if degradedSaveCalls != 2 || attempt.State != entity.InferenceModelUpgradeDegraded {
		t.Fatalf("transient degraded persistence was not retried: calls=%d attempt=%+v", degradedSaveCalls, attempt)
	}
}

func TestDegradedStatePersistenceSchedulesRetryAfterBoundedFailure(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State: entity.InferenceModelUpgradeRollingBack,
	}
	repository.save = func(attempt entity.InferenceModelUpgrade) error {
		if attempt.State == entity.InferenceModelUpgradeDegraded {
			return errors.New(apiError.FirestoreError)
		}
		return nil
	}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), &fakeUpgradeManager{})
	scheduled := 0
	service.Schedule = func(time.Duration, func()) { scheduled++ }
	if err := service.defaults(); err != nil {
		t.Fatal(err)
	}
	service.Config.OperationTimeout = 5 * time.Millisecond

	service.degrade(&upgradeOperation{attempt: repository.attempts["upgrade-1"]}, errors.New("rollback failed"))
	if scheduled != 1 {
		t.Fatalf("persistent degraded-state failure did not schedule recovery: %d", scheduled)
	}
}

func TestCandidateAdmissionFailureAttemptsAutomaticRollback(t *testing.T) {
	repository := newFakeUpgradeRepository()
	dockerClient := newFakeUpgradeDocker()
	manager := &fakeUpgradeManager{unblockErrors: map[string]error{testNewContainer: errors.New("admission unavailable")}}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), manager)

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeRolledBack || model.ContainerID != testOldContainer || model.ActiveUpgradeID != "" {
		t.Fatalf("candidate admission failure was not rolled back: attempt=%+v model=%+v", attempt, model)
	}
}

func TestRollbackWaitsForAcceptedCandidateProcessingBeforeCleanup(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.activeJobs = []bool{true, false}
	dockerClient := newFakeUpgradeDocker()
	apiClient := newFakeUpgradeAPI()
	apiClient.runtimeErr = errors.New("candidate verification failed")
	service := testUpgradeService(repository, dockerClient, apiClient, &fakeUpgradeManager{})

	started := startUpgrade(t, service)
	attempt, model := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeRolledBack || model.ContainerID != testOldContainer {
		t.Fatalf("upgrade was not rolled back: attempt=%+v model=%+v", attempt, model)
	}
	if repository.activeJobCalls != 2 || repository.activeJobVersion != "2.0.0" {
		t.Fatalf("candidate processing was not drained before cleanup: calls=%d version=%s",
			repository.activeJobCalls, repository.activeJobVersion,
		)
	}
	if !slices.Contains(dockerClient.removed, testNewContainer) {
		t.Fatalf("candidate was not removed after its accepted work completed: %v", dockerClient.removed)
	}
}

func TestRollbackRecoveryUsesCandidateVersionForPendingCleanup(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.activeJobsErr = errors.New("processing state unavailable")
	dockerClient := newFakeUpgradeDocker()
	apiClient := newFakeUpgradeAPI()
	apiClient.runtimeErr = errors.New("candidate verification failed")
	service := testUpgradeService(repository, dockerClient, apiClient, &fakeUpgradeManager{})

	started := startUpgrade(t, service)
	attempt, _ := repository.snapshot(started.ID)
	if attempt.State != entity.InferenceModelUpgradeRolledBack || attempt.PendingCleanupContainerID != testNewContainer {
		t.Fatalf("candidate cleanup was not retained for recovery: %+v", attempt)
	}
	if slices.Contains(dockerClient.removed, testNewContainer) {
		t.Fatalf("candidate was removed without a reliable processing-state check: %v", dockerClient.removed)
	}

	repository.activeJobsErr = nil
	repository.activeJobs = []bool{false}
	if err := service.RecoverInferenceModelUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt, _ = repository.snapshot(started.ID)
	if attempt.PendingCleanupContainerID != "" || !slices.Contains(dockerClient.removed, testNewContainer) {
		t.Fatalf("candidate cleanup recovery did not complete: attempt=%+v removed=%v", attempt, dockerClient.removed)
	}
	if repository.activeJobVersion != "2.0.0" {
		t.Fatalf("recovery checked the wrong deployment version: %s", repository.activeJobVersion)
	}
}

func TestUpgradeStateTransitionsRejectSkipsAndTerminalChanges(t *testing.T) {
	if validTransition(entity.InferenceModelUpgradeQueued, entity.InferenceModelUpgradeDraining) {
		t.Fatal("queued upgrade skipped directly to draining")
	}
	if !validTransition(entity.InferenceModelUpgradeActivating, entity.InferenceModelUpgradeVerifying) {
		t.Fatal("expected activation transition was rejected")
	}
	if validTransition(entity.InferenceModelUpgradeSucceeded, entity.InferenceModelUpgradePulling) {
		t.Fatal("terminal upgrade was allowed to restart")
	}
}

func TestRecoverCompletesDurableContainerCleanupWithoutRollback(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ContainerID = testNewContainer
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State: entity.InferenceModelUpgradeSucceeded, PendingCleanupContainerID: testOldContainer,
		Previous:  entity.InferenceModelDeployment{ContainerID: testOldContainer},
		Candidate: &entity.InferenceModelDeployment{ContainerID: testNewContainer},
	}
	dockerClient := newFakeUpgradeDocker()
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})

	if err := service.RecoverInferenceModelUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt, model := repository.snapshot("upgrade-1")
	if attempt.State != entity.InferenceModelUpgradeSucceeded || attempt.PendingCleanupContainerID != "" || model.ContainerID != testNewContainer {
		t.Fatalf("terminal cleanup changed deployment outcome: attempt=%+v model=%+v", attempt, model)
	}
	if !reflect.DeepEqual(dockerClient.removed, []string{testOldContainer}) {
		t.Fatalf("pending container was not removed: %v", dockerClient.removed)
	}
}

func TestRecoverRetainsCleanupContainerWhileAcceptedProcessingJobIsActive(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ContainerID = testNewContainer
	repository.activeJobs = []bool{true}
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1", ModelName: "Model",
		State: entity.InferenceModelUpgradeSucceeded, PendingCleanupContainerID: testOldContainer,
		Previous: entity.InferenceModelDeployment{ContainerID: testOldContainer, ModelVersion: "1.0.0"},
	}
	dockerClient := newFakeUpgradeDocker()
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})

	if err := service.RecoverInferenceModelUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt, _ := repository.snapshot("upgrade-1")
	if attempt.PendingCleanupContainerID != testOldContainer {
		t.Fatalf("active processing cleanup identity was cleared: %+v", attempt)
	}
	if slices.Contains(dockerClient.removed, testOldContainer) {
		t.Fatalf("active processing container was removed: %v", dockerClient.removed)
	}
}

func TestRecoverFindsCandidateCreatedBeforeAuditPersistence(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ActiveUpgradeID = "upgrade-1"
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State:    entity.InferenceModelUpgradeValidating,
		Previous: entity.InferenceModelDeployment{ContainerID: testOldContainer, ImageReference: "heartwisehub/model:1.0.0"},
	}
	dockerClient := newFakeUpgradeDocker()
	dockerClient.containers[testNewContainer] = docker.GetContainerInfoResult{
		ID: testNewContainer, Name: "/" + candidateContainerName("upgrade-1"), Running: true,
	}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})

	if err := service.RecoverInferenceModelUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt, _ := repository.snapshot("upgrade-1")
	if attempt.State != entity.InferenceModelUpgradeFailed || !reflect.DeepEqual(dockerClient.removed, []string{testNewContainer}) {
		t.Fatalf("orphan candidate was not recovered: attempt=%+v removed=%v", attempt, dockerClient.removed)
	}
}

func TestRecoverFinalizesRollbackAfterRegistrationWasAlreadyRestored(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ActiveUpgradeID = "upgrade-1"
	repository.jobContainerID = testNewContainer
	repository.jobVersion = "2.0.0"
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State: entity.InferenceModelUpgradeRollingBack,
		Previous: entity.InferenceModelDeployment{
			ContainerID: testOldContainer, ImageReference: "heartwisehub/model:1.0.0", ModelVersion: "1.0.0",
		},
		Candidate: &entity.InferenceModelDeployment{
			ContainerID: testNewContainer, ImageReference: "heartwisehub/model:2.0.0", ModelVersion: "2.0.0",
		},
	}
	dockerClient := newFakeUpgradeDocker()
	dockerClient.containers[testNewContainer] = docker.GetContainerInfoResult{ID: testNewContainer, Name: "/candidate", Running: true}
	service := testUpgradeService(repository, dockerClient, newFakeUpgradeAPI(), &fakeUpgradeManager{})

	if err := service.RecoverInferenceModelUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt, model := repository.snapshot("upgrade-1")
	if attempt.State != entity.InferenceModelUpgradeRolledBack || model.ActiveUpgradeID != "" || model.ContainerID != testOldContainer {
		t.Fatalf("rollback finalization was not recovered: attempt=%+v model=%+v", attempt, model)
	}
	if repository.jobContainerID != testOldContainer || repository.jobVersion != "1.0.0" {
		t.Fatalf("ingestion target rollback was not recovered: container=%s version=%s", repository.jobContainerID, repository.jobVersion)
	}
}

func TestRecoverBlocksRestoredDeploymentWhenRoutingRestorationFails(t *testing.T) {
	repository := newFakeUpgradeRepository()
	repository.model.ActiveUpgradeID = "upgrade-1"
	repository.jobContainerID = testNewContainer
	repository.jobVersion = "2.0.0"
	repository.retargetErr = errors.New("routing database unavailable")
	repository.attempts["upgrade-1"] = entity.InferenceModelUpgrade{
		ID: "upgrade-1", TenantID: "tenant-1", ModelID: "model-1",
		State: entity.InferenceModelUpgradeRollingBack,
		Previous: entity.InferenceModelDeployment{
			ContainerID: testOldContainer, ImageReference: "heartwisehub/model:1.0.0", ModelVersion: "1.0.0",
		},
		Candidate: &entity.InferenceModelDeployment{
			ContainerID: testNewContainer, ImageReference: "heartwisehub/model:2.0.0", ModelVersion: "2.0.0",
		},
	}
	manager := &fakeUpgradeManager{}
	service := testUpgradeService(repository, newFakeUpgradeDocker(), newFakeUpgradeAPI(), manager)

	if err := service.RecoverInferenceModelUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt, model := repository.snapshot("upgrade-1")
	if attempt.State != entity.InferenceModelUpgradeDegraded || model.ActiveUpgradeID != "upgrade-1" {
		t.Fatalf("failed routing restoration did not retain the degraded lock: attempt=%+v model=%+v", attempt, model)
	}
	if !reflect.DeepEqual(manager.calls, []string{"block:" + testOldContainer}) {
		t.Fatalf("restored deployment was not blocked before degrading: %v", manager.calls)
	}
}
