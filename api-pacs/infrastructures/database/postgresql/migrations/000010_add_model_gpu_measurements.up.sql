-- Per-container GPU memory observed by the model manager sampler. One row per
-- model container; a replaced container gets a new row and the old one is
-- deleted once it leaves the manager inventory.
CREATE TABLE IF NOT EXISTS model_gpu_measurements (
    container_id TEXT PRIMARY KEY,
    model_name   TEXT NOT NULL DEFAULT '',
    image_id     TEXT NOT NULL DEFAULT '',
    gpu_uuid     TEXT NOT NULL DEFAULT '',
    last_mib     INTEGER NOT NULL DEFAULT 0,
    peak_mib     INTEGER NOT NULL DEFAULT 0,
    resident_mib INTEGER NOT NULL DEFAULT 0,
    samples      INTEGER NOT NULL DEFAULT 0,
    overshoots   INTEGER NOT NULL DEFAULT 0,
    sampled_at   TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
