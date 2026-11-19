-- Retrieve moves api-pacs tracks (PACS-433, PACS-435): saved so a restart
-- keeps retrying them, and background moves waiting for a free Orthanc slot.
-- job_id is the ID the caller keeps (the first Orthanc job, or a
-- pacsai-move-* ID for a background move that waited in api-pacs).
CREATE TABLE IF NOT EXISTS orthanc_tracked_moves (
    job_id varchar(128) PRIMARY KEY,
    modality_id varchar(128) NOT NULL,
    request jsonb NOT NULL,
    current_job_id varchar(128) NOT NULL DEFAULT '',
    parts jsonb NOT NULL,
    expected_instances integer NOT NULL DEFAULT 0,
    attempts integer NOT NULL DEFAULT 0,
    retry_at timestamptz NULL,
    verified boolean NOT NULL DEFAULT false,
    background boolean NOT NULL DEFAULT false,
    queued boolean NOT NULL DEFAULT false,
    queued_at timestamptz NULL,
    priority integer NOT NULL DEFAULT 0,
    done boolean NOT NULL DEFAULT false,
    final_state varchar(32) NOT NULL DEFAULT '',
    final_error text NOT NULL DEFAULT '',
    completion_time varchar(32) NOT NULL DEFAULT '',
    tracked_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS orthanc_tracked_moves_tracked_at_idx ON orthanc_tracked_moves (tracked_at);
