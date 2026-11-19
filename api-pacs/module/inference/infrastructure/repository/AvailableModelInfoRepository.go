package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
)

// AvailableModelInfoDB is the context-aware part of *sqlx.DB the store uses.
type AvailableModelInfoDB interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
}

// AvailableModelInfoRepository keeps the last good model info per container
// in Postgres (table model_available_info, migration 000014), so a model that
// is busy right after an api-pacs restart stays in the available-models list.
type AvailableModelInfoRepository struct {
	DB AvailableModelInfoDB
}

var errAvailableModelInfoNotConfigured = errors.New("available-model info storage is not configured")

const selectAvailableModelInfo = `SELECT info FROM model_available_info WHERE container_id = $1`

const upsertAvailableModelInfo = `
	INSERT INTO model_available_info (container_id, info, updated_at)
	VALUES ($1, $2::jsonb, NOW())
	ON CONFLICT (container_id) DO UPDATE SET info = EXCLUDED.info, updated_at = NOW()`

const deleteAvailableModelInfoExcept = `DELETE FROM model_available_info WHERE NOT (container_id = ANY($1::text[]))`

func (repository *AvailableModelInfoRepository) LoadAvailableModelInfo(ctx context.Context, containerID string) ([]byte, bool, error) {
	if repository.DB == nil {
		return nil, false, errAvailableModelInfoNotConfigured
	}
	var info []byte
	if err := repository.DB.GetContext(ctx, &info, selectAvailableModelInfo, containerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return info, true, nil
}

func (repository *AvailableModelInfoRepository) SaveAvailableModelInfo(ctx context.Context, containerID string, info []byte) error {
	if repository.DB == nil {
		return errAvailableModelInfoNotConfigured
	}
	_, err := repository.DB.ExecContext(ctx, upsertAvailableModelInfo, containerID, string(info))
	return err
}

func (repository *AvailableModelInfoRepository) DeleteAvailableModelInfoExcept(ctx context.Context, containerIDs []string) error {
	if repository.DB == nil {
		return errAvailableModelInfoNotConfigured
	}
	if containerIDs == nil {
		containerIDs = []string{}
	}
	_, err := repository.DB.ExecContext(ctx, deleteAvailableModelInfoExcept, pq.Array(containerIDs))
	return err
}
