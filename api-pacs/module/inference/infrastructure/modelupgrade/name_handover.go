package modelupgrade

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	dockererrdefs "github.com/containerd/errdefs"

	docker "api-pacs/infrastructures/providers/sdk/docker/types"
)

// A completed move leaves two containers: the drained one, still running under
// the model's name, and its replacement under a "-candidate-" name. The name
// hand-over gives the replacement the model's name, because the host's tooling
// (rollout scripts, docker logs) addresses a model container by that name:
//
//  1. stop the old container; it is kept, never removed, as the rollback copy
//  2. rename it to <name>-pre-move-<first 12 characters of its ID>
//  3. rename the new container to <name>
//
// The gateway itself identifies a model by container ID, but reaches it over
// HTTP by the name Docker reports for that ID. Steps 2 and 3 therefore run
// while admission to the new container is held, so no request is on its way to
// a name that is about to change, and the name is confirmed to answer before
// admission reopens.
//
// The hand-over runs after activation, when the move can no longer roll back.
// Nothing in it fails the move: every outcome is logged as event=move_names.

const (
	nameHandOverBackupInfix = "-pre-move-"
	// nameHandOverBackupAttempts bounds the search for a free backup name.
	nameHandOverBackupAttempts = 20
	nameHandOverVerifyPoll     = 250 * time.Millisecond
)

const (
	nameHandOverDone    = "ok"
	nameHandOverSkipped = "skipped"
	nameHandOverFailed  = "failed"
)

// admissionHolder is implemented by a manager that can keep requests for one
// model waiting, without unloading it, while its container is renamed.
type admissionHolder interface {
	HoldAdmissionForRename(context.Context, string, func(context.Context) (string, error)) error
}

type nameHandOver struct {
	// Name is the model's stable container name, the one the old container
	// carried when the move started.
	Name, ModelID, OldID, NewID string
}

type nameHandOverResult struct {
	Outcome string
	// Step names where a skipped or failed hand-over stopped.
	Step string
	Err  error
	// NewName is what the new container is called when the hand-over returns.
	NewName string
	// BackupName is what the old container is called when the hand-over
	// returns; empty when the old container no longer exists.
	BackupName string
	// Reverted reports that the renames already made were undone.
	Reverted bool
}

func trimContainerName(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "/")
}

// handOverContainerNames is idempotent: every step looks at what Docker reports
// now, so it can be run again after a crash or a partial failure.
func (coordinator *UpgradeCoordinator) handOverContainerNames(ctx context.Context, request nameHandOver) (result nameHandOverResult) {
	started := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Outcome, result.Step, result.Err = nameHandOverFailed, "panic", fmt.Errorf("%v", recovered)
		}
		log.Printf(
			"[model-deployment] event=move_names outcome=%s step=%q model_id=%s name=%q new_container=%.12s new_name=%q old_container=%.12s old_name=%q reverted=%t duration_ms=%d err=%q",
			result.Outcome, result.Step, request.ModelID, request.Name, request.NewID, result.NewName,
			request.OldID, result.BackupName, result.Reverted, time.Since(started).Milliseconds(), errorText(result.Err),
		)
	}()
	fail := func(step string, err error) nameHandOverResult {
		result.Outcome, result.Step, result.Err = nameHandOverFailed, step, err
		return result
	}
	skip := func(step string, err error) nameHandOverResult {
		result.Outcome, result.Step, result.Err = nameHandOverSkipped, step, err
		return result
	}

	name := trimContainerName(request.Name)
	if name == "" || request.NewID == "" || request.OldID == request.NewID || len(request.OldID) < 12 {
		return skip("identify", errors.New("a container name and two distinct container IDs are required"))
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coordinator.config.NameHandOverTimeout)
	defer cancel()

	created, err := coordinator.docker.GetContainerInfo(ctx, request.NewID)
	if err != nil {
		return fail("inspect_new", err)
	}
	result.NewName = trimContainerName(created.Name)
	if created.ID != request.NewID || result.NewName == "" {
		return skip("inspect_new", errors.New("the new container could not be identified"))
	}
	if created.Lifecycle != docker.LifecycleRunning {
		return skip("inspect_new", fmt.Errorf("the new container is %s", created.Lifecycle))
	}
	previous, err := coordinator.docker.GetContainerInfo(ctx, request.OldID)
	oldExists := err == nil
	switch {
	case err != nil && !dockererrdefs.IsNotFound(err):
		return fail("inspect_old", err)
	case err == nil && previous.ID != request.OldID:
		return skip("inspect_old", errors.New("the old container could not be identified"))
	case err == nil:
		result.BackupName = trimContainerName(previous.Name)
	}

	renameNew := result.NewName != name
	renameOld := oldExists && result.BackupName == name
	if renameNew && !renameOld {
		// The old container no longer holds the name (an earlier hand-over
		// already moved it aside, or it was removed). The name must be free:
		// a container this move does not know about is never touched.
		holder, holderErr := coordinator.docker.GetContainerInfo(ctx, name)
		switch {
		case holderErr == nil:
			return skip("name_in_use", fmt.Errorf("container %.12s holds the name", holder.ID))
		case !dockererrdefs.IsNotFound(holderErr):
			return fail("inspect_name", holderErr)
		}
	}
	var holder admissionHolder
	if renameNew {
		var ok bool
		if holder, ok = coordinator.manager.(admissionHolder); !ok {
			return skip("hold_admission", errors.New("the model manager cannot hold admission during a rename"))
		}
	}
	backup := ""
	if renameOld {
		if backup, err = coordinator.freeBackupName(ctx, name, request.OldID); err != nil {
			return fail("backup_name", err)
		}
	}

	// 1. The old container was drained and unloaded before activation and its
	// slot in the manager stays closed; nothing addresses it any more.
	if oldExists && previous.Lifecycle != docker.LifecycleStopped {
		if err = coordinator.docker.StopContainer(ctx, request.OldID); err != nil {
			return fail("stop_old", err)
		}
	}
	if !renameNew {
		return result.done()
	}

	// 2 and 3, with admission to the new container held.
	step := "hold_admission"
	err = holder.HoldAdmissionForRename(ctx, request.NewID, func(ctx context.Context) (string, error) {
		candidateName := result.NewName
		if renameOld {
			step = "rename_old"
			if renameErr := coordinator.docker.RenameContainer(ctx, request.OldID, backup); renameErr != nil {
				return "", renameErr
			}
			result.BackupName = backup
		}
		// revert puts the names back as the move left them. It does not use
		// the hand-over context, which may be the reason for the failure.
		revert := func(newRenamed bool) {
			revertCtx, cancelRevert := context.WithTimeout(context.WithoutCancel(ctx), coordinator.config.RollbackTimeout)
			defer cancelRevert()
			result.Reverted = true
			if newRenamed {
				if revertErr := coordinator.docker.RenameContainer(revertCtx, request.NewID, candidateName); revertErr != nil {
					result.Reverted = false
					return // the old container cannot take the name back either
				}
				result.NewName = candidateName
			}
			if renameOld {
				if revertErr := coordinator.docker.RenameContainer(revertCtx, request.OldID, name); revertErr != nil {
					result.Reverted = false
					return
				}
				result.BackupName = name
			}
		}
		step = "rename_new"
		if renameErr := coordinator.docker.RenameContainer(ctx, request.NewID, name); renameErr != nil {
			revert(false)
			return "", renameErr
		}
		result.NewName = name
		// The old container is stopped, so only the new one can answer to the
		// name. Requests are sent to it as soon as admission reopens.
		step = "verify_name"
		if verifyErr := coordinator.verifyContainerName(ctx, name); verifyErr != nil {
			revert(true)
			// Whatever the new container is called now is what the manager
			// must address it by.
			if result.NewName == name {
				return name, nil
			}
			return "", verifyErr
		}
		step = ""
		return name, nil
	})
	if step != "" {
		if err == nil {
			err = errors.New("the new container did not answer to the name and could not be renamed back")
		}
		return fail(step, err)
	}
	return result.done()
}

func (result nameHandOverResult) done() nameHandOverResult {
	result.Outcome, result.Step, result.Err = nameHandOverDone, "", nil
	return result
}

// freeBackupName returns a name no container holds for the rollback copy. A
// name that is taken belongs to a container left by something else and is
// never reused or renamed.
func (coordinator *UpgradeCoordinator) freeBackupName(ctx context.Context, name, oldID string) (string, error) {
	base := name + nameHandOverBackupInfix + oldID[:12]
	for attempt := 1; attempt <= nameHandOverBackupAttempts; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		_, err := coordinator.docker.GetContainerInfo(ctx, candidate)
		if dockererrdefs.IsNotFound(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no free backup name after %d attempts from %s", nameHandOverBackupAttempts, base)
}

// verifyContainerName waits until the model answers under its new name.
func (coordinator *UpgradeCoordinator) verifyContainerName(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, coordinator.config.NameVerifyTimeout)
	defer cancel()
	for {
		_, err := coordinator.api.GetModelRuntime(ctx, name)
		if err == nil {
			return nil
		}
		timer := time.NewTimer(nameHandOverVerifyPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("model did not answer as %s: %w", name, err)
		case <-timer.C:
		}
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
