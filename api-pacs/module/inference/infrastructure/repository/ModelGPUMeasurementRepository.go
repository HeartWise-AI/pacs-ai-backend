package repository

import (
	"context"
	"errors"
	"time"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	"api-pacs/module/inference/infrastructure/modelmanager"
)

// ModelGPUMeasurementRepository persists the model manager's measured GPU
// memory in Postgres (table model_gpu_measurements, migration 000010).
type ModelGPUMeasurementRepository struct {
	postgresqlTypes.PostgresSQLDBHandlerInterface
}

type modelGPUMeasurementRow struct {
	ContainerID string     `db:"container_id"`
	ModelName   string     `db:"model_name"`
	ImageID     string     `db:"image_id"`
	GPUUUID     string     `db:"gpu_uuid"`
	LastMiB     int        `db:"last_mib"`
	PeakMiB     int        `db:"peak_mib"`
	ResidentMiB int        `db:"resident_mib"`
	Samples     int        `db:"samples"`
	Overshoots  int        `db:"overshoots"`
	SampledAt   *time.Time `db:"sampled_at"`
}

const selectModelGPUMeasurements = `
	SELECT container_id, model_name, image_id, gpu_uuid, last_mib, peak_mib, resident_mib, samples, overshoots, sampled_at
	FROM model_gpu_measurements`

const upsertModelGPUMeasurement = `
	INSERT INTO model_gpu_measurements
		(container_id, model_name, image_id, gpu_uuid, last_mib, peak_mib, resident_mib, samples, overshoots, sampled_at, updated_at)
	VALUES
		(:container_id, :model_name, :image_id, :gpu_uuid, :last_mib, :peak_mib, :resident_mib, :samples, :overshoots, :sampled_at, NOW())
	ON CONFLICT (container_id) DO UPDATE SET
		model_name = EXCLUDED.model_name,
		image_id = EXCLUDED.image_id,
		gpu_uuid = EXCLUDED.gpu_uuid,
		last_mib = EXCLUDED.last_mib,
		peak_mib = EXCLUDED.peak_mib,
		resident_mib = EXCLUDED.resident_mib,
		samples = EXCLUDED.samples,
		overshoots = EXCLUDED.overshoots,
		sampled_at = EXCLUDED.sampled_at,
		updated_at = NOW()`

const deleteModelGPUMeasurement = `DELETE FROM model_gpu_measurements WHERE container_id = :container_id`

func (repository *ModelGPUMeasurementRepository) Load(context.Context) ([]modelmanager.Measurement, error) {
	if repository.PostgresSQLDBHandlerInterface == nil {
		return nil, errors.New("GPU measurement storage is not configured")
	}
	var rows []modelGPUMeasurementRow
	if err := repository.PostgresSQLDBHandlerInterface.Query(selectModelGPUMeasurements, map[string]interface{}{}, &rows); err != nil {
		return nil, err
	}
	measurements := make([]modelmanager.Measurement, 0, len(rows))
	for _, row := range rows {
		item := modelmanager.Measurement{
			ContainerID: row.ContainerID, Name: row.ModelName, ImageID: row.ImageID, GPUUUID: row.GPUUUID,
			LastMiB: row.LastMiB, PeakMiB: row.PeakMiB, ResidentMiB: row.ResidentMiB, Samples: row.Samples, Overshoots: row.Overshoots,
		}
		if row.SampledAt != nil {
			item.SampledAt = *row.SampledAt
		}
		measurements = append(measurements, item)
	}
	return measurements, nil
}

func (repository *ModelGPUMeasurementRepository) Save(_ context.Context, upsert []modelmanager.Measurement, remove []string) error {
	if repository.PostgresSQLDBHandlerInterface == nil {
		return errors.New("GPU measurement storage is not configured")
	}
	for _, item := range upsert {
		row := modelGPUMeasurementRow{
			ContainerID: item.ContainerID, ModelName: item.Name, ImageID: item.ImageID, GPUUUID: item.GPUUUID,
			LastMiB: item.LastMiB, PeakMiB: item.PeakMiB, ResidentMiB: item.ResidentMiB, Samples: item.Samples, Overshoots: item.Overshoots,
		}
		if !item.SampledAt.IsZero() {
			sampledAt := item.SampledAt.UTC()
			row.SampledAt = &sampledAt
		}
		if _, err := repository.PostgresSQLDBHandlerInterface.Execute(upsertModelGPUMeasurement, row); err != nil {
			return err
		}
	}
	for _, id := range remove {
		if _, err := repository.PostgresSQLDBHandlerInterface.Execute(deleteModelGPUMeasurement, map[string]interface{}{"container_id": id}); err != nil {
			return err
		}
	}
	return nil
}
