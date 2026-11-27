package repository

import (
	"context"
	"errors"
	"testing"

	"api-pacs/module/inference/domain/entity"
	domain "api-pacs/module/inference/domain/repository"
	"github.com/stretchr/testify/require"
)

type activeRegistryReaderFake struct {
	domain.InferenceQueryRepositoryInterface
	err error
}

func (r *activeRegistryReaderFake) ListActiveModelRegistrations(ctx context.Context) ([]entity.InferenceModel, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return []entity.InferenceModel{{ID: "document-id"}}, r.err
}

func TestActiveRegistryCircuitBreakerSupportsOptionalReader(t *testing.T) {
	reader := &activeRegistryReaderFake{}
	r := &InferenceQueryRepositoryCircuitBreaker{InferenceQueryRepositoryInterface: reader}
	models, err := r.ListActiveModelRegistrations(context.Background())
	require.NoError(t, err)
	require.Equal(t, "document-id", models[0].ID)
	reader.err = errors.New("repository unavailable")
	_, err = r.ListActiveModelRegistrations(context.Background())
	require.ErrorIs(t, err, reader.err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = r.ListActiveModelRegistrations(ctx)
	require.Error(t, err)
	_, err = (&InferenceQueryRepositoryCircuitBreaker{}).ListActiveModelRegistrations(context.Background())
	require.Error(t, err)
}
