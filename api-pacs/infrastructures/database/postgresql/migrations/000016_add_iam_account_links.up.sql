-- Accounts of another app (CardioAgent) linked to a PACS-AI user (CA-268).
-- A link is created only after the user proves their PACS-AI identity (or by
-- an admin); a sign-in handoff from that app then opens a PACS-AI session for
-- the linked user. Keyed on the other app's user ID, never on the email.
CREATE TABLE IF NOT EXISTS iam_account_links (
    id            BIGSERIAL PRIMARY KEY,
    provider      TEXT        NOT NULL,
    external_uid  TEXT        NOT NULL,
    tenant_id     TEXT        NOT NULL,
    pacs_user_id  TEXT        NOT NULL,
    email_at_link TEXT        NOT NULL DEFAULT '',
    linked_by     TEXT        NOT NULL,
    linked_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at    TIMESTAMPTZ NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_iam_account_links_external
    ON iam_account_links (provider, tenant_id, external_uid) WHERE revoked_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS ux_iam_account_links_pacs_user
    ON iam_account_links (provider, tenant_id, pacs_user_id) WHERE revoked_at IS NULL;
