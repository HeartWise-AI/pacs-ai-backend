package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// AccountLink links a user of another app (provider) to a PACS-AI user
// (table iam_account_links, migration 000016).
type AccountLink struct {
	Provider    string `db:"provider"`
	ExternalUID string `db:"external_uid"`
	TenantID    string `db:"tenant_id"`
	PacsUserID  string `db:"pacs_user_id"`
	EmailAtLink string `db:"email_at_link"`
	LinkedBy    string `db:"linked_by"`
}

// AccountLinkRepository stores account links in Postgres.
type AccountLinkRepository struct {
	DB *sqlx.DB
}

var errAccountLinksNotConfigured = errors.New("account link storage is not configured")

const selectActiveAccountLink = `
	SELECT provider, external_uid, tenant_id, pacs_user_id, email_at_link, linked_by
	FROM iam_account_links
	WHERE provider = $1 AND tenant_id = $2 AND external_uid = $3 AND revoked_at IS NULL`

const revokeConflictingAccountLinks = `
	UPDATE iam_account_links SET revoked_at = NOW()
	WHERE revoked_at IS NULL AND provider = $1 AND tenant_id = $2
	  AND (external_uid = $3 OR pacs_user_id = $4)`

const insertAccountLink = `
	INSERT INTO iam_account_links (provider, external_uid, tenant_id, pacs_user_id, email_at_link, linked_by)
	VALUES ($1, $2, $3, $4, $5, $6)`

// FindActiveAccountLink returns the active link of an external user in a tenant.
func (repository *AccountLinkRepository) FindActiveAccountLink(ctx context.Context, provider, tenantID, externalUID string) (AccountLink, bool, error) {
	if repository == nil || repository.DB == nil {
		return AccountLink{}, false, errAccountLinksNotConfigured
	}
	var link AccountLink
	err := repository.DB.GetContext(ctx, &link, selectActiveAccountLink, provider, tenantID, externalUID)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountLink{}, false, nil
	}
	if err != nil {
		return AccountLink{}, false, err
	}
	return link, true, nil
}

// SaveAccountLink replaces any active link of the same external user or of
// the same PACS-AI user (one link per side), in one transaction.
func (repository *AccountLinkRepository) SaveAccountLink(ctx context.Context, link AccountLink) error {
	if repository == nil || repository.DB == nil {
		return errAccountLinksNotConfigured
	}
	tx, err := repository.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, revokeConflictingAccountLinks, link.Provider, link.TenantID, link.ExternalUID, link.PacsUserID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, insertAccountLink, link.Provider, link.ExternalUID, link.TenantID, link.PacsUserID, link.EmailAtLink, link.LinkedBy); err != nil {
		return err
	}
	return tx.Commit()
}
