CREATE TYPE inference_ingestion_job_status AS ENUM ('RUNNING', 'STOPPED');

CREATE TABLE
    inference_ingestion_jobs (
        id varchar(50) NOT NULL,
        tenant_id varchar(50) NOT NULL,
        dicom_modality varchar(255) NOT NULL,
        container_id varchar(255) NOT NULL,
        model_id varchar(255) NOT NULL,
        model_name varchar(255) NOT NULL,
        model_version varchar(255) NOT NULL,
        modalities varchar(255)[] NOT NULL,
        interval_in_minutes int NOT NULL,
        schedule_start_timestamp timestamp NOT NULL,
        schedule_end_timestamp timestamp NOT NULL,
        status inference_ingestion_job_status NOT NULL,
        last_executed_at timestamp NULL,
        created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
        updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
        PRIMARY KEY (id)
    );

-- function to update the updated_at column
CREATE OR REPLACE FUNCTION func_update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

-- trigger to automatically update updated_at on row updates
CREATE TRIGGER trigger_update_inference_ingestion_jobs_updated_at
    BEFORE UPDATE ON inference_ingestion_jobs
    FOR EACH ROW
    EXECUTE FUNCTION func_update_updated_at();
