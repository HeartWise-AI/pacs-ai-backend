-- Last good model info (/info and /facts) per model container, so a model
-- that is busy right after an api-pacs restart stays in the available-models
-- list. A replaced container gets a new row; rows of containers that are no
-- longer registered are deleted by api-pacs.
CREATE TABLE IF NOT EXISTS model_available_info (
    container_id TEXT PRIMARY KEY,
    info         JSONB NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
