package repository

import (
	"context"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Runs against a scratch Postgres that has migration 000016 applied:
// TEST_POSTGRES_URL=postgres://... go test ./module/iam/infrastructure/repository/
func TestAccountLinkRepositoryKeepsOneActiveLinkPerSide(t *testing.T) {
	url := os.Getenv("TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("TEST_POSTGRES_URL not set")
	}
	db, err := sqlx.Connect("postgres", url)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`DELETE FROM iam_account_links`)
	require.NoError(t, err)
	repository := &AccountLinkRepository{DB: db}
	ctx := context.Background()

	_, found, err := repository.FindActiveAccountLink(ctx, "cardioagent", "t1", "fb-1")
	require.NoError(t, err)
	require.False(t, found)

	first := AccountLink{Provider: "cardioagent", ExternalUID: "fb-1", TenantID: "t1", PacsUserID: "u1", EmailAtLink: "a@b.c", LinkedBy: "self-password"}
	require.NoError(t, repository.SaveAccountLink(ctx, first))
	link, found, err := repository.FindActiveAccountLink(ctx, "cardioagent", "t1", "fb-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, first, link)

	// Relinking the same external user to another PACS-AI user replaces the link.
	second := first
	second.PacsUserID = "u2"
	require.NoError(t, repository.SaveAccountLink(ctx, second))
	link, _, _ = repository.FindActiveAccountLink(ctx, "cardioagent", "t1", "fb-1")
	require.Equal(t, "u2", link.PacsUserID)

	// Linking another external user to u2 revokes fb-1's link.
	third := AccountLink{Provider: "cardioagent", ExternalUID: "fb-2", TenantID: "t1", PacsUserID: "u2", LinkedBy: "admin"}
	require.NoError(t, repository.SaveAccountLink(ctx, third))
	_, found, _ = repository.FindActiveAccountLink(ctx, "cardioagent", "t1", "fb-1")
	require.False(t, found)

	var active, total int
	require.NoError(t, db.Get(&active, `SELECT count(*) FROM iam_account_links WHERE revoked_at IS NULL`))
	require.NoError(t, db.Get(&total, `SELECT count(*) FROM iam_account_links`))
	require.Equal(t, 1, active)
	require.Equal(t, 3, total)
}
