package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAvailableModelInfoRepositoryRoundTrip(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	repository := &AvailableModelInfoRepository{DB: sqlx.NewDb(database, "sqlmock")}
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO model_available_info").
		WithArgs("container-a", `{"ModelID":"a"}`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repository.SaveAvailableModelInfo(ctx, "container-a", []byte(`{"ModelID":"a"}`)))

	mock.ExpectQuery("SELECT info FROM model_available_info").WithArgs("container-a").
		WillReturnRows(sqlmock.NewRows([]string{"info"}).AddRow([]byte(`{"ModelID":"a"}`)))
	info, ok, err := repository.LoadAvailableModelInfo(ctx, "container-a")
	require.NoError(t, err)
	require.True(t, ok)
	require.JSONEq(t, `{"ModelID":"a"}`, string(info))

	mock.ExpectQuery("SELECT info FROM model_available_info").WithArgs("missing").
		WillReturnRows(sqlmock.NewRows([]string{"info"}))
	_, ok, err = repository.LoadAvailableModelInfo(ctx, "missing")
	require.NoError(t, err)
	require.False(t, ok, "no row is not an error")

	mock.ExpectExec("DELETE FROM model_available_info").
		WithArgs(pq.Array([]string{"container-a"})).
		WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, repository.DeleteAvailableModelInfoExcept(ctx, []string{"container-a"}))

	mock.ExpectExec("DELETE FROM model_available_info").
		WithArgs(pq.Array([]string{})).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repository.DeleteAvailableModelInfoExcept(ctx, nil))

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvailableModelInfoRepositoryWithoutDatabase(t *testing.T) {
	repository := &AvailableModelInfoRepository{}
	_, _, err := repository.LoadAvailableModelInfo(context.Background(), "c")
	require.Error(t, err)
	require.Error(t, repository.SaveAvailableModelInfo(context.Background(), "c", []byte(`{}`)))
	require.Error(t, repository.DeleteAvailableModelInfoExcept(context.Background(), nil))
}
