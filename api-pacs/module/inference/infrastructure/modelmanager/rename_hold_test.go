package modelmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
)

func managedName(m *Manager, n int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.models[id(n)].model.Name
}

func TestRenameHoldWaitsForAdmittedWorkAndKeepsNewRequestsWaiting(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	b.predict = func(ctx context.Context, _ Model, _ types.PredictRequest) (types.PredictResponse, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return types.PredictResponse{}, ctx.Err()
		case <-release:
			return types.PredictResponse{Success: true}, nil
		}
	}
	m := manager(t, b, 100)
	first := request(m, 1)
	<-started

	renaming := make(chan struct{})
	finishRename := make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- m.HoldAdmissionForRename(context.Background(), id(1), func(context.Context) (string, error) {
			close(renaming)
			<-finishRename
			// Docker reports the new name from now on.
			b.mu.Lock()
			renamed := b.models[id(1)]
			renamed.Name = "renamed"
			b.models[id(1)] = renamed
			b.mu.Unlock()
			return "/renamed", nil
		})
	}()
	select {
	case <-renaming:
		t.Fatal("the container was renamed while a request was admitted")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	receive(t, first)
	<-renaming

	// A request that arrives during the rename waits; it is neither rejected
	// nor sent to the container.
	second := request(m, 1)
	until(t, func() bool { return m.Snapshot().QueueDepth == 1 })
	select {
	case err := <-second:
		t.Fatalf("request did not wait for the rename: %v", err)
	case <-started:
		t.Fatal("request reached the container during the rename")
	case <-time.After(20 * time.Millisecond):
	}
	snapshot := m.Snapshot().Models[id(1)]
	if !snapshot.Loaded || snapshot.Blocked || snapshot.ReservedMiB == 0 {
		t.Fatalf("the hold changed the model's residency: %+v", snapshot)
	}
	close(finishRename)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if name := managedName(m, 1); name != "renamed" {
		t.Fatalf("the manager addresses the container as %q", name)
	}
	<-started
	close(release)
	receive(t, second)
	if len(b.unloads) != 0 {
		t.Fatalf("the hold unloaded the model: %v", b.unloads)
	}
}

func TestRenameHoldKeepsTheKnownNameWhenTheRenameFails(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	m := manager(t, b, 100)
	receive(t, request(m, 1))
	failure := errors.New("rename refused")
	err := m.HoldAdmissionForRename(context.Background(), id(1), func(context.Context) (string, error) {
		return "half-renamed", failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("expected the rename failure, got %v", err)
	}
	if name := managedName(m, 1); name != "1" {
		t.Fatalf("a failed rename changed the known name to %q", name)
	}
	// The model is admitted again afterwards.
	receive(t, request(m, 1))
}

func TestRenameHoldGivesUpWhenTheModelStaysBusy(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	b.predict = func(context.Context, Model, types.PredictRequest) (types.PredictResponse, error) {
		started <- struct{}{}
		<-release
		return types.PredictResponse{Success: true}, nil
	}
	m := manager(t, b, 100)
	busy := request(m, 1)
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	called := false
	err := m.HoldAdmissionForRename(ctx, id(1), func(context.Context) (string, error) {
		called = true
		return "renamed", nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("a busy model was renamed: err=%v called=%t", err, called)
	}
	close(release)
	receive(t, busy)
	if err = m.HoldAdmissionForRename(context.Background(), id(2), func(context.Context) (string, error) { return "x", nil }); err == nil {
		t.Fatal("a container outside the inventory was held")
	}
}
