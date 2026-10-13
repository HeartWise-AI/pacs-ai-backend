package modelupgrade

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
)

const (
	testActiveContainerID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testOldImageReference = "heartwisehub/model:1.0.0"
)

type coordinatorDocker struct {
	events       *[]string
	created      docker.CreateCandidateContainer
	createErr    error
	removeErr    error
	removed      []string
	activeInfo   docker.GetContainerInfoResult
	candidateID  string
	pullStarted  chan struct{}
	continuePull chan struct{}
}

func (fake *coordinatorDocker) PullImage(context.Context, string) error {
	fake.record("docker.pull")
	if fake.pullStarted != nil {
		close(fake.pullStarted)
	}
	if fake.continuePull != nil {
		<-fake.continuePull
	}
	return nil
}

func (fake *coordinatorDocker) InspectImage(context.Context, string) (docker.InspectImageResult, error) {
	return docker.InspectImageResult{}, nil
}

func (fake *coordinatorDocker) CreateCandidateContainer(
	_ context.Context,
	request docker.CreateCandidateContainer,
) (string, error) {
	fake.record("docker.create")
	fake.created = request
	return fake.candidateID, fake.createErr
}

func (fake *coordinatorDocker) GetContainerInfo(context.Context, string) (docker.GetContainerInfoResult, error) {
	fake.record("docker.active")
	return fake.activeInfo, nil
}

func (fake *coordinatorDocker) StartContainer(context.Context, string) error { return nil }
func (fake *coordinatorDocker) StopContainer(context.Context, string) error  { return nil }

func (fake *coordinatorDocker) RemoveContainer(_ context.Context, id string) error {
	fake.record("docker.remove:" + id)
	fake.removed = append(fake.removed, id)
	return fake.removeErr
}

func (fake *coordinatorDocker) record(event string) { *fake.events = append(*fake.events, event) }

type coordinatorAPI struct {
	events     *[]string
	modelInfo  api.GetModelInfoResponse
	runtime    api.ModelRuntime
	runtimeErr error
}

func (fake *coordinatorAPI) GetModelInfo(context.Context, string) (api.GetModelInfoResponse, error) {
	*fake.events = append(*fake.events, "api.active-model-info")
	return fake.modelInfo, nil
}

func (fake *coordinatorAPI) GetModelRuntime(context.Context, string) (api.ModelRuntime, error) {
	*fake.events = append(*fake.events, "api.candidate-runtime")
	return fake.runtime, fake.runtimeErr
}

type coordinatorValidator struct {
	events      *[]string
	validateErr error
}

func (fake *coordinatorValidator) ValidateImageReference(reference, _ string) (string, error) {
	*fake.events = append(*fake.events, "validator.reference")
	return reference, nil
}

func (fake *coordinatorValidator) ResolveImage(context.Context, string, string) (ResolvedCandidateImage, error) {
	*fake.events = append(*fake.events, "validator.resolve")
	return validResolvedImage(), nil
}

func (fake *coordinatorValidator) ValidateContainer(
	_ context.Context,
	request ValidateCandidateContainer,
) (ValidatedCandidate, error) {
	*fake.events = append(*fake.events, "validator.container")
	if fake.validateErr != nil {
		return ValidatedCandidate{}, fake.validateErr
	}
	return ValidatedCandidate{
		ContainerID:    request.ContainerID,
		ContainerName:  "candidate-model",
		ImageReference: request.Image.Reference,
		ImageDigest:    request.Image.Digest,
		LocalImageID:   request.Image.LocalImageID,
		ModelInfo:      validCandidateModelInfo(),
		Runtime:        api.ModelRuntime{State: api.RuntimeUnloaded},
	}, nil
}

type coordinatorManager struct {
	events *[]string
	fail   map[string]error
}

func (fake *coordinatorManager) BlockAndDrain(_ context.Context, id string) error {
	return fake.operation("drain:" + id)
}

func (fake *coordinatorManager) ReplaceDrained(oldID, newID string) error {
	return fake.operation("replace:" + oldID + ":" + newID)
}

func (fake *coordinatorManager) PrepareBlocked(_ context.Context, id string) error {
	return fake.operation("prepare:" + id)
}

func (fake *coordinatorManager) Unblock(id string) error {
	return fake.operation("unblock:" + id)
}

func (fake *coordinatorManager) operation(event string) error {
	*fake.events = append(*fake.events, "manager."+event)
	return fake.fail[event]
}

type coordinatorModels struct {
	events *[]string
	model  entity.InferenceModel
}

func (fake *coordinatorModels) SelectInferenceModelByID(context.Context, string) (entity.InferenceModel, error) {
	*fake.events = append(*fake.events, "models.select")
	return fake.model, nil
}

type coordinatorRegistrations struct {
	events     *[]string
	previous   entity.InferenceModelDeployment
	replaceErr error
	restoreErr error
	replaced   repositoryTypes.ReplaceInferenceModelRegistration
	restored   repositoryTypes.RestoreInferenceModelRegistration
}

func (fake *coordinatorRegistrations) ReplaceInferenceModelRegistration(
	_ context.Context,
	request repositoryTypes.ReplaceInferenceModelRegistration,
) (entity.InferenceModelDeployment, error) {
	*fake.events = append(*fake.events, "registration.replace")
	fake.replaced = request
	return fake.previous, fake.replaceErr
}

func (fake *coordinatorRegistrations) RestoreInferenceModelRegistration(
	_ context.Context,
	request repositoryTypes.RestoreInferenceModelRegistration,
) error {
	*fake.events = append(*fake.events, "registration.restore")
	fake.restored = request
	return fake.restoreErr
}

type coordinatorFixture struct {
	coordinator   *UpgradeCoordinator
	docker        *coordinatorDocker
	api           *coordinatorAPI
	validator     *coordinatorValidator
	manager       *coordinatorManager
	registrations *coordinatorRegistrations
	events        *[]string
}

func newCoordinatorFixture(t *testing.T) coordinatorFixture {
	t.Helper()
	events := []string{}
	dockerFake := &coordinatorDocker{
		events:      &events,
		candidateID: testCandidateID,
		activeInfo: docker.GetContainerInfoResult{
			ID: testActiveContainerID, Name: "/active-model", ImageID: "sha256:old",
		},
	}
	apiFake := &coordinatorAPI{
		events:    &events,
		modelInfo: api.GetModelInfoResponse{Success: true, Data: api.ModelInfo{ModelID: "model-1", Version: "1.0.0"}},
		runtime:   api.ModelRuntime{State: api.RuntimeReady, Loaded: true},
	}
	validatorFake := &coordinatorValidator{events: &events}
	managerFake := &coordinatorManager{events: &events, fail: map[string]error{}}
	modelsFake := &coordinatorModels{events: &events, model: entity.InferenceModel{
		ID:          "registration-1",
		TenantID:    "tenant-1",
		ContainerID: testActiveContainerID,
		DockerImage: testOldImageReference,
		Envs:        []string{"TOKEN=secret"},
		OutputMode:  entity.OutputModeHTML,
	}}
	registrationsFake := &coordinatorRegistrations{
		events: &events,
		previous: entity.InferenceModelDeployment{
			ContainerID: testActiveContainerID, ImageReference: testOldImageReference,
		},
	}
	coordinator, err := NewUpgradeCoordinator(
		dockerFake,
		apiFake,
		validatorFake,
		managerFake,
		modelsFake,
		registrationsFake,
		CoordinatorConfig{DrainTimeout: time.Second, RollbackTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	return coordinatorFixture{
		coordinator: coordinator, docker: dockerFake, api: apiFake, validator: validatorFake,
		manager: managerFake, registrations: registrationsFake, events: &events,
	}
}

func (fixture coordinatorFixture) upgrade(t *testing.T) (UpgradeResult, error) {
	t.Helper()
	return fixture.coordinator.Upgrade(context.Background(), UpgradeRequest{
		TenantID: "tenant-1", ModelID: "registration-1", ImageReference: "heartwisehub/model:2.0.0",
	})
}

func TestUpgradeCoordinatorActivatesValidatedCandidate(t *testing.T) {
	fixture := newCoordinatorFixture(t)

	result, err := fixture.upgrade(t)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"models.select", "docker.active", "api.active-model-info", "validator.reference",
		"docker.pull", "validator.resolve", "docker.create", "validator.container",
		"manager.drain:" + testActiveContainerID,
		"manager.replace:" + testActiveContainerID + ":" + testCandidateID,
		"manager.prepare:" + testCandidateID, "api.candidate-runtime", "registration.replace",
		"manager.unblock:" + testCandidateID,
	}
	if !reflect.DeepEqual(*fixture.events, wantEvents) {
		t.Fatalf("events:\n got: %v\nwant: %v", *fixture.events, wantEvents)
	}
	if fixture.docker.created.Image != testImageID {
		t.Fatalf("candidate was created from %q instead of immutable local image %q", fixture.docker.created.Image, testImageID)
	}
	if fixture.docker.created.ActiveContainerID != testActiveContainerID ||
		!reflect.DeepEqual(fixture.docker.created.Envs, []string{"TOKEN=secret"}) {
		t.Fatalf("candidate did not inherit active runtime settings: %+v", fixture.docker.created)
	}
	if len(fixture.docker.removed) != 0 {
		t.Fatalf("successful upgrade removed rollback baseline: %v", fixture.docker.removed)
	}
	if result.Active.ContainerID != testCandidateID || result.Previous.ContainerID != testActiveContainerID {
		t.Fatalf("unexpected upgrade result: %+v", result)
	}
}

func TestUpgradeCoordinatorDoesNotDrainWhenCandidateValidationFails(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.validator.validateErr = errors.New("candidate rejected")

	_, err := fixture.upgrade(t)
	assertUpgradeStage(t, err, UpgradeStageValidate)
	assertEventAbsent(t, *fixture.events, "manager.")
	assertEventsEndWith(t, *fixture.events, "validator.container", "docker.remove:"+testCandidateID)
}

func TestUpgradeCoordinatorRollsBackManagerWhenCandidatePreparationFails(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.manager.fail["prepare:"+testCandidateID] = errors.New("load failed")

	_, err := fixture.upgrade(t)
	assertUpgradeStage(t, err, UpgradeStagePrepare)
	assertEventsEndWith(t, *fixture.events,
		"manager.prepare:"+testCandidateID,
		"manager.drain:"+testCandidateID,
		"manager.replace:"+testCandidateID+":"+testActiveContainerID,
		"manager.prepare:"+testActiveContainerID,
		"manager.unblock:"+testActiveContainerID,
		"docker.remove:"+testCandidateID,
	)
}

func TestUpgradeCoordinatorRollsBackManagerWhenRegistrationSwitchFails(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.registrations.replaceErr = errors.New("registration conflict")

	_, err := fixture.upgrade(t)
	assertUpgradeStage(t, err, UpgradeStageActivate)
	assertEventAbsent(t, *fixture.events, "registration.restore")
	assertEventsEndWith(t, *fixture.events,
		"registration.replace",
		"manager.drain:"+testCandidateID,
		"manager.replace:"+testCandidateID+":"+testActiveContainerID,
		"manager.prepare:"+testActiveContainerID,
		"manager.unblock:"+testActiveContainerID,
		"docker.remove:"+testCandidateID,
	)
}

func TestUpgradeCoordinatorRestoresRegistrationWhenCandidateCannotBeUnblocked(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.manager.fail["unblock:"+testCandidateID] = errors.New("candidate unsafe")

	_, err := fixture.upgrade(t)
	assertUpgradeStage(t, err, UpgradeStageActivate)
	assertEventsEndWith(t, *fixture.events,
		"registration.replace",
		"manager.unblock:"+testCandidateID,
		"manager.drain:"+testCandidateID,
		"manager.replace:"+testCandidateID+":"+testActiveContainerID,
		"manager.prepare:"+testActiveContainerID,
		"registration.restore",
		"manager.unblock:"+testActiveContainerID,
		"docker.remove:"+testCandidateID,
	)
	if fixture.registrations.restored.Previous.ContainerID != testActiveContainerID ||
		fixture.registrations.restored.ExpectedContainerID != testCandidateID {
		t.Fatalf("incorrect registration rollback: %+v", fixture.registrations.restored)
	}
}

func TestUpgradeCoordinatorRetainsCandidateWhenRollbackCannotBeConfirmed(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	rollbackErr := errors.New("candidate drain failed")
	fixture.manager.fail["prepare:"+testCandidateID] = errors.New("load failed")
	fixture.manager.fail["drain:"+testCandidateID] = rollbackErr

	_, err := fixture.upgrade(t)
	upgradeErr := assertUpgradeStage(t, err, UpgradeStagePrepare)
	if !errors.Is(upgradeErr.RollbackErr, rollbackErr) {
		t.Fatalf("rollback error was not preserved: %v", upgradeErr.RollbackErr)
	}
	assertEventAbsent(t, *fixture.events, "docker.remove:")
}

func TestUpgradeCoordinatorRejectsConcurrentUpgradeForSameModel(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.docker.pullStarted = make(chan struct{})
	fixture.docker.continuePull = make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := fixture.upgrade(t)
		firstDone <- err
	}()

	<-fixture.docker.pullStarted
	_, err := fixture.upgrade(t)
	upgradeErr := assertUpgradeStage(t, err, UpgradeStageResolveActive)
	if upgradeErr.Err.Error() != "INFERENCE_UPGRADE_CONFLICT" {
		t.Fatalf("concurrent upgrade returned %v", upgradeErr)
	}

	close(fixture.docker.continuePull)
	if err = <-firstDone; err != nil {
		t.Fatalf("first upgrade failed: %v", err)
	}
}

func assertUpgradeStage(t *testing.T, err error, stage UpgradeStage) *UpgradeError {
	t.Helper()
	var upgradeErr *UpgradeError
	if !errors.As(err, &upgradeErr) || upgradeErr.Stage != stage {
		t.Fatalf("upgrade error = %v, want stage %s", err, stage)
	}
	return upgradeErr
}

func assertEventAbsent(t *testing.T, events []string, prefix string) {
	t.Helper()
	for _, event := range events {
		if len(event) >= len(prefix) && event[:len(prefix)] == prefix {
			t.Fatalf("unexpected event %q in %v", event, events)
		}
	}
}

func assertEventsEndWith(t *testing.T, events []string, suffix ...string) {
	t.Helper()
	if len(events) < len(suffix) || !reflect.DeepEqual(events[len(events)-len(suffix):], suffix) {
		t.Fatalf("events %v do not end with %v", events, suffix)
	}
}
