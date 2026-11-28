package modelupgrade

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	dockererrdefs "github.com/containerd/errdefs"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
)

const (
	handOverName       = "active-model"
	handOverCandidate  = "candidate-model"
	handOverBackup     = "active-model-pre-move-aaaaaaaaaaaa"
	handOverStrangerID = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// handOverDocker is a Docker engine reduced to what a hand-over touches: names
// are unique, a rename to a held name is refused, and containers are found by
// ID or by exact name. fail makes one call fail, keyed like the recorded call.
type handOverDocker struct {
	*coordinatorDocker
	containers map[string]*docker.GetContainerInfoResult
	calls      []string
	fail       map[string]error
}

func (fake *handOverDocker) find(reference string) *docker.GetContainerInfoResult {
	if container, ok := fake.containers[reference]; ok {
		return container
	}
	for _, container := range fake.containers {
		if strings.TrimPrefix(container.Name, "/") == reference {
			return container
		}
	}
	return nil
}

func (fake *handOverDocker) GetContainerInfo(_ context.Context, reference string) (docker.GetContainerInfoResult, error) {
	if err := fake.fail["inspect:"+reference]; err != nil {
		return docker.GetContainerInfoResult{}, err
	}
	if container := fake.find(reference); container != nil {
		return *container, nil
	}
	return docker.GetContainerInfoResult{}, fmt.Errorf("%w: %w", dockererrdefs.ErrNotFound, errors.New("No such container: "+reference))
}

func (fake *handOverDocker) StopContainer(_ context.Context, id string) error {
	fake.calls = append(fake.calls, "stop:"+id[:4])
	if err := fake.fail["stop:"+id[:4]]; err != nil {
		return err
	}
	container := fake.find(id)
	if container == nil {
		return fmt.Errorf("%w: %w", dockererrdefs.ErrNotFound, errors.New("No such container: "+id))
	}
	container.Lifecycle = docker.LifecycleStopped
	return nil
}

func (fake *handOverDocker) RenameContainer(_ context.Context, id, name string) error {
	call := "rename:" + id[:4] + ":" + name
	fake.calls = append(fake.calls, call)
	if err := fake.fail[call]; err != nil {
		return err
	}
	container := fake.find(id)
	if container == nil {
		return fmt.Errorf("%w: %w", dockererrdefs.ErrNotFound, errors.New("No such container: "+id))
	}
	if holder := fake.find(name); holder != nil {
		return fmt.Errorf("%w: %w", dockererrdefs.ErrConflict, fmt.Errorf("the container name %q is already in use by %s", name, holder.ID))
	}
	container.Name = "/" + name
	return nil
}

func (fake *handOverDocker) names() map[string]string {
	names := map[string]string{}
	for id, container := range fake.containers {
		names[id[:4]] = strings.TrimPrefix(container.Name, "/") + "/" + string(container.Lifecycle)
	}
	return names
}

// handOverManager holds admission the way the model manager does and records
// what the new container is addressed by afterwards.
type handOverManager struct {
	*idleCoordinatorManager
	docker  *handOverDocker
	holdErr error
	known   string
}

func (manager *handOverManager) HoldAdmissionForRename(ctx context.Context, id string, rename func(context.Context) (string, error)) error {
	if manager.holdErr != nil {
		return manager.holdErr
	}
	manager.docker.calls = append(manager.docker.calls, "hold:"+id[:4])
	name, err := rename(ctx)
	manager.docker.calls = append(manager.docker.calls, "release:"+id[:4])
	if err == nil && name != "" {
		manager.known = name
	}
	return err
}

// handOverAPI answers only under names a running container holds.
type handOverAPI struct {
	*coordinatorAPI
	docker *handOverDocker
	deaf   map[string]bool
}

func (fake *handOverAPI) GetModelRuntime(ctx context.Context, name string) (api.ModelRuntime, error) {
	container := fake.docker.find(name)
	if fake.deaf[name] || container == nil || container.Lifecycle != docker.LifecycleRunning {
		return api.ModelRuntime{}, errors.New("dial tcp: lookup " + name + ": no such host")
	}
	return fake.coordinatorAPI.GetModelRuntime(ctx, name)
}

type handOverFixture struct {
	coordinatorFixture
	engine  *handOverDocker
	manager *handOverManager
	api     *handOverAPI
}

// handOverMoveFixture is a move whose Docker engine keeps track of names.
func handOverMoveFixture(t *testing.T) handOverFixture {
	t.Helper()
	f := moveFixture(t)
	active := f.docker.activeInfo
	active.Lifecycle = docker.LifecycleRunning
	candidate := f.docker.infos[testCandidateID]
	candidate.Name, candidate.Lifecycle = "/"+handOverCandidate, docker.LifecycleRunning
	engine := &handOverDocker{
		coordinatorDocker: f.docker, fail: map[string]error{},
		containers: map[string]*docker.GetContainerInfoResult{testActiveContainerID: &active, testCandidateID: &candidate},
	}
	manager := &handOverManager{
		idleCoordinatorManager: &idleCoordinatorManager{placementCoordinatorManager: f.coordinator.manager.(*placementCoordinatorManager)},
		docker:                 engine,
	}
	apiFake := &handOverAPI{coordinatorAPI: f.api, docker: engine, deaf: map[string]bool{}}
	f.coordinator.docker, f.coordinator.manager, f.coordinator.api = engine, manager, apiFake
	f.coordinator.config.NameVerifyTimeout = 20 * time.Millisecond
	return handOverFixture{coordinatorFixture: f, engine: engine, manager: manager, api: apiFake}
}

func (f handOverFixture) handOver() nameHandOverResult {
	return f.coordinator.handOverContainerNames(context.Background(), nameHandOver{
		Name: handOverName, ModelID: "registration-1", OldID: testActiveContainerID, NewID: testCandidateID,
	})
}

func (f handOverFixture) assertNames(t *testing.T, old, created string) {
	t.Helper()
	want := map[string]string{testActiveContainerID[:4]: old, testCandidateID[:4]: created}
	got := f.engine.names()
	for id, name := range want {
		if got[id] != name {
			t.Fatalf("containers:\n got: %v\nwant: %v\ncalls: %v", got, want, f.engine.calls)
		}
	}
}

var handOverHappyCalls = []string{
	"stop:aaaa", "hold:cand", "rename:aaaa:" + handOverBackup, "rename:cand:" + handOverName, "release:cand",
}

func TestMoveHandsTheNameOverToTheNewContainer(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		f := handOverMoveFixture(t)
		result, err := f.move(automatic)
		if err != nil {
			t.Fatal(err)
		}
		// The old container is stopped and kept; nothing is removed.
		f.assertNames(t, handOverBackup+"/stopped", handOverName+"/running")
		if !reflect.DeepEqual(f.engine.calls, handOverHappyCalls) {
			t.Fatalf("calls:\n got: %v\nwant: %v", f.engine.calls, handOverHappyCalls)
		}
		if len(f.docker.removed) != 0 {
			t.Fatalf("the rollback copy was removed: %v", f.docker.removed)
		}
		if result.ContainerName != handOverName || f.manager.known != handOverName {
			t.Fatalf("the new container is reported as %q and addressed as %q", result.ContainerName, f.manager.known)
		}
		// Names are touched only once the move can no longer roll back.
		unblocked := slices.Index(*f.events, "manager.unblock:"+testCandidateID)
		if unblocked < 0 || unblocked < slices.Index(*f.events, "registration.replace") {
			t.Fatalf("hand-over ran before activation: %v", *f.events)
		}
		if result.Active.ContainerID != testCandidateID || f.registrations.replaced.Deployment.ContainerID != testCandidateID {
			t.Fatalf("the registration does not follow the container ID: %+v", result.Active)
		}
	}
}

func TestUpgradeLeavesContainerNamesAlone(t *testing.T) {
	f := newCoordinatorFixture(t)
	active := f.docker.activeInfo
	active.Lifecycle = docker.LifecycleRunning
	engine := &handOverDocker{
		coordinatorDocker: f.docker, fail: map[string]error{},
		containers: map[string]*docker.GetContainerInfoResult{
			testActiveContainerID: &active,
			testCandidateID:       {ID: testCandidateID, Name: "/" + handOverCandidate, Lifecycle: docker.LifecycleRunning},
		},
	}
	f.coordinator.docker = engine
	f.coordinator.manager = &handOverManager{
		idleCoordinatorManager: &idleCoordinatorManager{placementCoordinatorManager: &placementCoordinatorManager{coordinatorManager: f.manager}},
		docker:                 engine,
	}
	result, err := f.upgrade(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(engine.calls) != 0 || result.ContainerName != handOverCandidate {
		t.Fatalf("an upgrade changed container names: %v (reported %q)", engine.calls, result.ContainerName)
	}
}

func TestNameHandOverFailureLeavesTheMoveCompleted(t *testing.T) {
	refused := errors.New("engine refused")
	tests := []struct {
		name           string
		arrange        func(handOverFixture)
		step           string
		old, created   string
		reverted       bool
		containerName  string
		forbiddenCalls []string
	}{
		{
			name:    "old container cannot be stopped",
			arrange: func(f handOverFixture) { f.engine.fail["stop:aaaa"] = refused },
			step:    "stop_old", old: handOverName + "/running", created: handOverCandidate + "/running",
			containerName: handOverCandidate, forbiddenCalls: []string{"rename:", "hold:"},
		},
		{
			name:    "admission cannot be held",
			arrange: func(f handOverFixture) { f.manager.holdErr = context.DeadlineExceeded },
			step:    "hold_admission", old: handOverName + "/stopped", created: handOverCandidate + "/running",
			containerName: handOverCandidate, forbiddenCalls: []string{"rename:"},
		},
		{
			name:    "old container cannot be renamed",
			arrange: func(f handOverFixture) { f.engine.fail["rename:aaaa:"+handOverBackup] = refused },
			step:    "rename_old", old: handOverName + "/stopped", created: handOverCandidate + "/running",
			containerName: handOverCandidate, forbiddenCalls: []string{"rename:cand"},
		},
		{
			name:    "new container cannot be renamed",
			arrange: func(f handOverFixture) { f.engine.fail["rename:cand:"+handOverName] = refused },
			step:    "rename_new", old: handOverName + "/stopped", created: handOverCandidate + "/running",
			reverted: true, containerName: handOverCandidate,
		},
		{
			name:    "new container does not answer to the name",
			arrange: func(f handOverFixture) { f.api.deaf[handOverName] = true },
			step:    "verify_name", old: handOverName + "/stopped", created: handOverCandidate + "/running",
			reverted: true, containerName: handOverCandidate,
		},
		{
			name: "renames cannot be undone",
			arrange: func(f handOverFixture) {
				f.api.deaf[handOverName] = true
				f.engine.fail["rename:cand:"+handOverCandidate] = refused
			},
			step: "verify_name", old: handOverBackup + "/stopped", created: handOverName + "/running",
			containerName: handOverName,
		},
		{
			name:    "old container cannot be inspected",
			arrange: func(f handOverFixture) { f.engine.fail["inspect:"+testActiveContainerID] = refused },
			step:    "inspect_old", old: handOverName + "/running", created: handOverCandidate + "/running",
			containerName: handOverCandidate, forbiddenCalls: []string{"stop:", "rename:", "hold:"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := handOverMoveFixture(t)
			tc.arrange(f)
			// An inspection failure is armed only once the move is activated,
			// so that it hits the hand-over and not the stages before it.
			armed := map[string]error{}
			for key, value := range f.engine.fail {
				if strings.HasPrefix(key, "inspect:") {
					armed[key] = value
					delete(f.engine.fail, key)
				}
			}
			f.coordinator.manager = &armingManager{handOverManager: f.manager, arm: func() {
				for key, value := range armed {
					f.engine.fail[key] = value
				}
			}}
			result, err := f.move(false)
			if err != nil {
				t.Fatalf("a hand-over failure failed the move: %v", err)
			}
			if result.Active.ContainerID != testCandidateID || result.ContainerName != tc.containerName {
				t.Fatalf("unexpected move result: container=%s name=%q", result.Active.ContainerID, result.ContainerName)
			}
			for _, event := range *f.events {
				if event == "registration.restore" || strings.HasPrefix(event, "docker.remove") ||
					strings.HasPrefix(event, "manager.drain:"+testCandidateID) {
					t.Fatalf("a hand-over failure rolled the move back: %v", *f.events)
				}
			}
			f.assertNames(t, tc.old, tc.created)
			for _, call := range f.engine.calls {
				for _, forbidden := range tc.forbiddenCalls {
					if strings.HasPrefix(call, forbidden) {
						t.Fatalf("%s ran after the failure: %v", call, f.engine.calls)
					}
				}
			}

			// The same failure reported by the hand-over itself.
			f = handOverMoveFixture(t)
			tc.arrange(f)
			outcome := f.handOver()
			if outcome.Outcome != nameHandOverFailed || outcome.Step != tc.step || outcome.Err == nil || outcome.Reverted != tc.reverted {
				t.Fatalf("unexpected outcome: %+v", outcome)
			}
			if outcome.NewName != tc.containerName {
				t.Fatalf("the new container is reported as %q, want %q", outcome.NewName, tc.containerName)
			}

			// Once the cause is gone, running it again finishes the hand-over.
			f.engine.fail, f.api.deaf, f.manager.holdErr = map[string]error{}, map[string]bool{}, nil
			if outcome = f.handOver(); outcome.Outcome != nameHandOverDone {
				t.Fatalf("the hand-over did not recover: %+v calls=%v", outcome, f.engine.calls)
			}
			f.assertNames(t, handOverBackup+"/stopped", handOverName+"/running")
		})
	}
}

// armingManager arms inspection failures when the move is activated, so that
// they hit the hand-over and not the stages before it.
type armingManager struct {
	*handOverManager
	arm func()
}

func (manager *armingManager) Unblock(id string) error {
	err := manager.handOverManager.Unblock(id)
	manager.arm()
	return err
}

func TestNameHandOverNeverTakesABackupNameThatIsHeld(t *testing.T) {
	f := handOverMoveFixture(t)
	f.engine.containers[handOverStrangerID] = &docker.GetContainerInfoResult{
		ID: handOverStrangerID, Name: "/" + handOverBackup, Lifecycle: docker.LifecycleStopped,
	}
	second := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	f.engine.containers[second] = &docker.GetContainerInfoResult{
		ID: second, Name: "/" + handOverBackup + "-2", Lifecycle: docker.LifecycleStopped,
	}
	outcome := f.handOver()
	if outcome.Outcome != nameHandOverDone || outcome.BackupName != handOverBackup+"-3" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	f.assertNames(t, handOverBackup+"-3/stopped", handOverName+"/running")
	if got := f.engine.names(); got[handOverStrangerID[:4]] != handOverBackup+"/stopped" || got[second[:4]] != handOverBackup+"-2/stopped" {
		t.Fatalf("another container's name was touched: %v", got)
	}
	for _, call := range f.engine.calls {
		if strings.HasSuffix(call, ":"+handOverBackup) || strings.HasPrefix(call, "stop:cccc") || strings.HasPrefix(call, "rename:cccc") {
			t.Fatalf("the held backup name was used: %v", f.engine.calls)
		}
	}
}

func TestNameHandOverLeavesANameHeldByAnotherContainer(t *testing.T) {
	f := handOverMoveFixture(t)
	// An operator already moved the old container aside and started something
	// else under the model's name.
	f.engine.containers[testActiveContainerID].Name = "/operator-kept-copy"
	f.engine.containers[handOverStrangerID] = &docker.GetContainerInfoResult{
		ID: handOverStrangerID, Name: "/" + handOverName, Lifecycle: docker.LifecycleRunning,
	}
	outcome := f.handOver()
	if outcome.Outcome != nameHandOverSkipped || outcome.Step != "name_in_use" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if len(f.engine.calls) != 0 {
		t.Fatalf("containers were changed: %v", f.engine.calls)
	}
}

func TestNameHandOverIsIdempotent(t *testing.T) {
	f := handOverMoveFixture(t)
	if outcome := f.handOver(); outcome.Outcome != nameHandOverDone {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if !reflect.DeepEqual(f.engine.calls, handOverHappyCalls) {
		t.Fatalf("calls: %v", f.engine.calls)
	}
	f.engine.calls = nil
	outcome := f.handOver()
	if outcome.Outcome != nameHandOverDone || outcome.NewName != handOverName || outcome.BackupName != handOverBackup {
		t.Fatalf("unexpected second outcome: %+v", outcome)
	}
	if len(f.engine.calls) != 0 {
		t.Fatalf("a finished hand-over was repeated: %v", f.engine.calls)
	}
	f.assertNames(t, handOverBackup+"/stopped", handOverName+"/running")
}

func TestNameHandOverResumesFromEveryIntermediateState(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*handOverDocker)
		calls   []string
		old     string
	}{
		{
			name: "crashed after stopping the old container",
			arrange: func(engine *handOverDocker) {
				engine.containers[testActiveContainerID].Lifecycle = docker.LifecycleStopped
			},
			calls: handOverHappyCalls[1:], old: handOverBackup + "/stopped",
		},
		{
			name: "crashed between the two renames",
			arrange: func(engine *handOverDocker) {
				engine.containers[testActiveContainerID].Lifecycle = docker.LifecycleStopped
				engine.containers[testActiveContainerID].Name = "/" + handOverBackup
			},
			calls: []string{"hold:cand", "rename:cand:" + handOverName, "release:cand"}, old: handOverBackup + "/stopped",
		},
		{
			name: "names handed over by an operator, old container left running",
			arrange: func(engine *handOverDocker) {
				engine.containers[testActiveContainerID].Name = "/" + handOverBackup
				engine.containers[testCandidateID].Name = "/" + handOverName
			},
			calls: []string{"stop:aaaa"}, old: handOverBackup + "/stopped",
		},
		{
			name: "old container removed",
			arrange: func(engine *handOverDocker) {
				delete(engine.containers, testActiveContainerID)
			},
			calls: []string{"hold:cand", "rename:cand:" + handOverName, "release:cand"}, old: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := handOverMoveFixture(t)
			tc.arrange(f.engine)
			outcome := f.handOver()
			if outcome.Outcome != nameHandOverDone || outcome.NewName != handOverName {
				t.Fatalf("unexpected outcome: %+v", outcome)
			}
			if !reflect.DeepEqual(f.engine.calls, tc.calls) {
				t.Fatalf("calls:\n got: %v\nwant: %v", f.engine.calls, tc.calls)
			}
			f.assertNames(t, tc.old, handOverName+"/running")
		})
	}
}

func TestNameHandOverNeedsARunningNewContainerAndAManagerThatCanHold(t *testing.T) {
	f := handOverMoveFixture(t)
	f.engine.containers[testCandidateID].Lifecycle = docker.LifecycleStopped
	if outcome := f.handOver(); outcome.Outcome != nameHandOverSkipped || len(f.engine.calls) != 0 {
		t.Fatalf("a stopped new container was given the name: %+v calls=%v", outcome, f.engine.calls)
	}

	f = handOverMoveFixture(t)
	f.coordinator.manager = f.manager.idleCoordinatorManager
	if outcome := f.handOver(); outcome.Outcome != nameHandOverSkipped || outcome.Step != "hold_admission" || len(f.engine.calls) != 0 {
		t.Fatalf("names changed without an admission hold: %+v calls=%v", outcome, f.engine.calls)
	}
	f.assertNames(t, handOverName+"/running", handOverCandidate+"/running")
}
