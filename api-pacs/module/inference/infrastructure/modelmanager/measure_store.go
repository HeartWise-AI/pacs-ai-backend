package modelmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// FileMeasurementStore keeps observations in one small JSON file. It is the
// alternative to the Postgres store for hosts that set
// MODEL_GPU_MEASURE_STATE_PATH; the path must be on a volume that survives the
// gateway container.
type FileMeasurementStore struct {
	Path  string
	mu    sync.Mutex
	state map[string]Measurement
}

type measurementFile struct {
	Version      int           `json:"version"`
	Measurements []Measurement `json:"measurements"`
}

func (store *FileMeasurementStore) Load(context.Context) ([]Measurement, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.load()
}

func (store *FileMeasurementStore) load() ([]Measurement, error) {
	store.state = map[string]Measurement{}
	data, err := os.ReadFile(store.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file measurementFile
	if err = json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	for _, item := range file.Measurements {
		store.state[item.ContainerID] = item
	}
	return file.Measurements, nil
}

func (store *FileMeasurementStore) Save(_ context.Context, upsert []Measurement, remove []string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.state == nil {
		// Never replace an existing file with a partial state.
		if _, err := store.load(); err != nil {
			return err
		}
	}
	for _, item := range upsert {
		store.state[item.ContainerID] = item
	}
	for _, id := range remove {
		delete(store.state, id)
	}
	file := measurementFile{Version: 1, Measurements: make([]Measurement, 0, len(store.state))}
	for _, item := range store.state {
		file.Measurements = append(file.Measurements, item)
	}
	sort.Slice(file.Measurements, func(i, j int) bool { return file.Measurements[i].ContainerID < file.Measurements[j].ContainerID })
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	// Replace atomically so a crash never leaves a truncated state file.
	temporary, err := os.CreateTemp(filepath.Dir(store.Path), ".gpu-measurements-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(data); err == nil {
		err = temporary.Chmod(0o600)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary.Name(), store.Path)
}
