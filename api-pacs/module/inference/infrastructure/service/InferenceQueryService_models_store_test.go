package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
)

// memoryModelInfoStore is an AvailableModelInfoStore kept in a map.
type memoryModelInfoStore struct {
	mu      sync.Mutex
	rows    map[string][]byte
	saves   int
	deletes int
	loads   int
	err     error
}

func (store *memoryModelInfoStore) LoadAvailableModelInfo(_ context.Context, containerID string) ([]byte, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.loads++
	if store.err != nil {
		return nil, false, store.err
	}
	info, ok := store.rows[containerID]
	return info, ok, nil
}

func (store *memoryModelInfoStore) SaveAvailableModelInfo(_ context.Context, containerID string, info []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saves++
	if store.err != nil {
		return store.err
	}
	if store.rows == nil {
		store.rows = map[string][]byte{}
	}
	store.rows[containerID] = append([]byte(nil), info...)
	return nil
}

func (store *memoryModelInfoStore) DeleteAvailableModelInfoExcept(_ context.Context, containerIDs []string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.deletes++
	if store.err != nil {
		return store.err
	}
	keep := map[string]bool{}
	for _, id := range containerIDs {
		keep[id] = true
	}
	for id := range store.rows {
		if !keep[id] {
			delete(store.rows, id)
		}
	}
	return nil
}

func storedContainerIDs(store *memoryModelInfoStore) []string {
	out := make([]string, 0, len(store.rows))
	for id := range store.rows {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func TestGetInferenceAvailableModelsKeepsBusyModelAfterRestart(t *testing.T) {
	store := &memoryModelInfoStore{}
	before, _, _, _ := newBusyModelFixture()
	before.AvailableModelStore = store
	require.Equal(t, []string{"panecho-id", "jepa-id"}, modelIDs(t, before))
	require.Equal(t, []string{"container-a", "container-b"}, storedContainerIDs(store))

	// api-pacs restarts: a new service with an empty memory cache, and
	// panecho is busy with a study.
	after, repository, _, api := newBusyModelFixture()
	after.AvailableModelStore = store
	api.busyInfo["panecho"] = true
	repository.models[0].OutputMode = entity.OutputModeHTML

	result, err := after.GetInferenceAvailableModels(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "panecho-id", result[0].ModelID)
	require.Equal(t, "panecho", result[0].ContainerName)
	require.Equal(t, "panecho", result[0].ModelFacts.En["summary"])
	require.Equal(t, entity.OutputModeHTML, result[0].OutputMode, "output mode comes from the database, not the store")
}

func TestGetInferenceAvailableModelsSavesOnlyChangedInfo(t *testing.T) {
	store := &memoryModelInfoStore{}
	service, _, _, _ := newBusyModelFixture()
	service.AvailableModelStore = store

	modelIDs(t, service)
	modelIDs(t, service)
	modelIDs(t, service)
	require.Equal(t, 2, store.saves, "one save per container until its info changes")
	require.Equal(t, 1, store.deletes, "the registered set is applied once until it changes")
}

func TestGetInferenceAvailableModelsDropsStoredInfoOfRemovedContainers(t *testing.T) {
	store := &memoryModelInfoStore{rows: map[string][]byte{"container-old": []byte(`{"ContainerID":"container-old"}`)}}
	service, repository, _, _ := newBusyModelFixture()
	service.AvailableModelStore = store

	modelIDs(t, service)
	require.Equal(t, []string{"container-a", "container-b"}, storedContainerIDs(store), "a container no longer registered is dropped at the first list")

	repository.models = repository.models[:1]
	modelIDs(t, service)
	require.Equal(t, []string{"container-a"}, storedContainerIDs(store))
	require.Equal(t, 2, store.deletes)
}

func TestGetInferenceAvailableModelsIgnoresStoredInfoOfAnotherContainer(t *testing.T) {
	store := &memoryModelInfoStore{rows: map[string][]byte{"container-a": []byte(`{"ContainerID":"container-x","ModelID":"x"}`)}}
	service, _, _, api := newBusyModelFixture()
	service.AvailableModelStore = store
	api.busyInfo["panecho"] = true
	require.Equal(t, []string{"jepa-id"}, modelIDs(t, service))
}

func TestGetInferenceAvailableModelsPausesFailingStore(t *testing.T) {
	store := &memoryModelInfoStore{err: errors.New(`relation "model_available_info" does not exist`)}
	service, _, _, api := newBusyModelFixture()
	service.AvailableModelStore = store

	// The list still works from live answers and memory.
	require.Equal(t, []string{"panecho-id", "jepa-id"}, modelIDs(t, service))
	api.busyInfo["panecho"] = true
	require.Equal(t, []string{"panecho-id", "jepa-id"}, modelIDs(t, service))
	modelIDs(t, service)

	require.Equal(t, 1, store.saves+store.deletes+store.loads, "after the first failure the store is left alone for a while")
}
