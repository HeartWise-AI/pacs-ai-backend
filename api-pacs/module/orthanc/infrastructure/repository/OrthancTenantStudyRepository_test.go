package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/infrastructures/database/postgresql"
	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
)

// TestTenantStudyOwnershipSQL runs the repository's SQL against a scratch
// PostgreSQL: ORTHANC_TENANT_STUDY_TEST_PG=user:password@host:port/database.
// The migration is applied to that database; use a throwaway one.
func TestTenantStudyOwnershipSQL(t *testing.T) {
	dsn := os.Getenv("ORTHANC_TENANT_STUDY_TEST_PG")
	if dsn == "" {
		t.Skip("ORTHANC_TENANT_STUDY_TEST_PG not set")
	}
	credentials, location, _ := strings.Cut(dsn, "@")
	user, password, _ := strings.Cut(credentials, ":")
	hostPort, database, _ := strings.Cut(location, "/")
	host, port, _ := strings.Cut(hostPort, ":")

	db := &postgresql.PostgreSQLDBHandler{}
	require.NoError(t, db.Connect(postgresqlTypes.ConnectionParams{DBHost: host, DBPort: port, DBDatabase: database, DBUsername: user, DBPassword: password}))

	migration, err := os.ReadFile("../../../../infrastructures/database/postgresql/migrations/000017_add_orthanc_tenant_studies.up.sql")
	require.NoError(t, err)
	for _, statement := range []string{
		"DROP TABLE IF EXISTS orthanc_tenant_studies",
		"DROP TABLE IF EXISTS ingestion_candidates",
		"DROP TABLE IF EXISTS ingestion_processing_runs",
		string(migration),
		"CREATE TABLE ingestion_candidates (tenant_id varchar(50), study_instance_uid varchar(64))",
		"CREATE TABLE ingestion_processing_runs (tenant_id varchar(50), study_instance_uid varchar(64))",
		"INSERT INTO ingestion_candidates VALUES ('tenant-a', '1.2.9.2'), ('tenant-b', '1.2.9.2'), ('', '1.2.9.3')",
		"INSERT INTO ingestion_processing_runs VALUES ('tenant-a', '1.2.9.2'), ('tenant-c', '1.2.9.4')",
	} {
		_, err := db.Conn.Exec(statement)
		require.NoError(t, err, statement)
	}

	ctx := context.Background()
	multi := &OrthancTenantStudyRepository{PostgresSQLDBHandlerInterface: db}
	single := &OrthancTenantStudyRepository{PostgresSQLDBHandlerInterface: db, DefaultTenantID: "tenant-a"}
	all := []string{"1.2.9.1", "1.2.9.2", "1.2.9.3", "1.2.9.4"}

	// nothing owned yet
	got, err := multi.SelectTenantStudies(ctx, "tenant-a", all)
	require.NoError(t, err)
	require.Empty(t, got, "multi-tenant: an unowned study is nobody's")
	got, err = single.SelectTenantStudies(ctx, "tenant-a", all)
	require.NoError(t, err)
	require.ElementsMatch(t, all, got, "single tenant: the default tenant reads unowned studies")
	got, err = single.SelectTenantStudies(ctx, "tenant-made-up", all)
	require.NoError(t, err)
	require.Empty(t, got)
	every, err := single.SeesEveryStudy(ctx, "tenant-a")
	require.NoError(t, err)
	require.True(t, every)

	require.NoError(t, single.InsertTenantStudy(ctx, "tenant-b", "1.2.9.1"))
	require.NoError(t, single.InsertTenantStudy(ctx, "tenant-b", "1.2.9.1")) // idempotent
	require.NoError(t, single.InsertTenantStudy(ctx, "tenant-a", "1.2.9.3"))

	got, err = single.SelectTenantStudies(ctx, "tenant-a", all)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"1.2.9.2", "1.2.9.3", "1.2.9.4"}, got, "tenant B's study is no longer the default tenant's")
	got, err = single.SelectTenantStudies(ctx, "tenant-b", all)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"1.2.9.1"}, got)
	every, err = single.SeesEveryStudy(ctx, "tenant-a")
	require.NoError(t, err)
	require.False(t, every, "another tenant owns a study")
	every, err = multi.SeesEveryStudy(ctx, "tenant-a")
	require.NoError(t, err)
	require.False(t, every)

	owned, err := single.SelectOwnedStudies(ctx, all)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"1.2.9.1", "1.2.9.3"}, owned)

	evidence, err := single.SelectTenantStudyEvidence(ctx, all)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"tenant-a", "tenant-b"}, evidence["1.2.9.2"])
	require.ElementsMatch(t, []string{"tenant-c"}, evidence["1.2.9.4"])
	require.NotContains(t, evidence, "1.2.9.3", "an empty tenant is not evidence")
}
