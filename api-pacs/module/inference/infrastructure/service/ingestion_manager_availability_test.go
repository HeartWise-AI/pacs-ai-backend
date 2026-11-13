package service

import (
	"context"
	"testing"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/modelmanager"
	"github.com/stretchr/testify/require"
)

type unavailableIngestionManager struct {
	ManagedPredictor
	ready bool
}

func (manager *unavailableIngestionManager) Snapshot() modelmanager.Snapshot {
	return modelmanager.Snapshot{Ready: manager.ready}
}

func TestRetrievalWorkerPreservesQueuedWorkWhileManagerUnreconciled(t *testing.T) {
	// Nil repositories make any inventory access or mutation
	// fail the test. Retrieval must leave queued work untouched until admission is ready.
	manager := &unavailableIngestionManager{}
	command := &InferenceCommandService{ModelManager: manager}
	require.ErrorIs(t, command.ExecuteInferenceIngestionRetrievalWorker(context.Background()), modelmanager.ErrNotReconciled)
	manager.ready = true
	require.NoError(t, command.requireManagedIngestionReady())
}

type availabilityDiscoveryReader struct {
	repository.InferenceQueryRepositoryInterface
	called bool
}

func (reader *availabilityDiscoveryReader) SelectInferenceIngestionJobs(*string) ([]entity.InferenceIngestionJob, error) {
	reader.called = true
	return nil, nil
}

func TestDiscoveryStillPollsJobsWhileManagerUnreconciled(t *testing.T) {
	reader := &availabilityDiscoveryReader{}
	command := &InferenceCommandService{ModelManager: &unavailableIngestionManager{}, InferenceQueryRepositoryInterface: reader}
	require.NoError(t, command.ExecuteInferenceIngestionRunner(context.Background()))
	require.True(t, reader.called)
}

func TestUnmanagedWorkersRetainLegacyBehavior(t *testing.T) {
	require.NoError(t, (&InferenceCommandService{}).requireManagedIngestionReady())
}
