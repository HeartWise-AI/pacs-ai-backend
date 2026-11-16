package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/infrastructure/modelmanager"
)

func TestModelGPUMeasurementStatementsSurviveNamedQueryCompilation(t *testing.T) {
	// sqlx rewrites "::" in named statements; none may be used here.
	for _, statement := range []string{selectModelGPUMeasurements, upsertModelGPUMeasurement, deleteModelGPUMeasurement} {
		require.NotContains(t, statement, "::")
		compiled, _, err := sqlx.Named(statement, map[string]interface{}{
			"container_id": "c", "model_name": "", "image_id": "", "gpu_uuid": "", "last_mib": 0, "peak_mib": 0,
			"resident_mib": 0, "samples": 0, "overshoots": 0, "sampled_at": nil,
		})
		require.NoError(t, err)
		require.NotContains(t, compiled, ":")
	}
}

func TestModelGPUMeasurementRepositoryRoundTrip(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	repository := &ModelGPUMeasurementRepository{DB: sqlx.NewDb(database, "sqlmock")}
	id := strings.Repeat("a", 64)
	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

	mock.ExpectExec("INSERT INTO model_gpu_measurements").
		WithArgs(id, "DeepRV-CLIP", "sha256:image", "GPU-x", 3300, 3400, 3300, 7, 1, at).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM model_gpu_measurements").WithArgs("gone").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repository.Save(context.Background(), []modelmanager.Measurement{{
		ContainerID: id, Name: "DeepRV-CLIP", ImageID: "sha256:image", GPUUUID: "GPU-x", LastMiB: 3300, PeakMiB: 3400, ResidentMiB: 3300, Samples: 7, Overshoots: 1, SampledAt: at,
	}}, []string{"gone"}))

	mock.ExpectPrepare("SELECT container_id")
	mock.ExpectQuery("SELECT container_id").WillReturnRows(sqlmock.NewRows([]string{
		"container_id", "model_name", "image_id", "gpu_uuid", "last_mib", "peak_mib", "resident_mib", "samples", "overshoots", "sampled_at",
	}).AddRow(id, "DeepRV-CLIP", "sha256:image", "GPU-x", 3300, 3400, 3300, 7, 1, at).AddRow("other", "", "", "", 0, 0, 0, 0, 0, nil))
	loaded, err := repository.Load(context.Background())
	require.NoError(t, err)
	require.Len(t, loaded, 2)
	require.Equal(t, modelmanager.Measurement{ContainerID: id, Name: "DeepRV-CLIP", ImageID: "sha256:image", GPUUUID: "GPU-x", LastMiB: 3300, PeakMiB: 3400, ResidentMiB: 3300, Samples: 7, Overshoots: 1, SampledAt: at}, loaded[0])
	require.True(t, loaded[1].SampledAt.IsZero())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelGPUMeasurementRepositoryHonoursItsContext(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	repository := &ModelGPUMeasurementRepository{DB: sqlx.NewDb(database, "sqlmock")}

	// A database that does not answer must not hold the caller past its deadline.
	mock.ExpectPrepare("SELECT container_id")
	mock.ExpectQuery("SELECT container_id").WillDelayFor(5 * time.Second).WillReturnRows(sqlmock.NewRows([]string{"container_id"}))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = repository.Load(ctx)
	require.Error(t, err)
	require.Less(t, time.Since(started), 2*time.Second)

	mock.ExpectExec("INSERT INTO model_gpu_measurements").WillDelayFor(5 * time.Second).WillReturnResult(sqlmock.NewResult(0, 1))
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started = time.Now()
	err = repository.Save(ctx, []modelmanager.Measurement{{ContainerID: "c"}}, nil)
	require.Error(t, err)
	require.Less(t, time.Since(started), 2*time.Second)

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	require.Error(t, repository.Save(cancelled, nil, []string{"gone"}))
	_, err = (&ModelGPUMeasurementRepository{}).Load(context.Background())
	require.Error(t, err)
}
