CREATE TYPE inference_ingestion_run_status AS ENUM ('SUCCESS', 'FAILED');

CREATE TABLE
    inference_ingestion_run_results (
        id varchar(50) NOT NULL,
        job_id varchar(50) NOT NULL,
        study_instance_uid varchar(255) NOT NULL,
        inference_output JSON NULL,
        error_message varchar(500) NULL,
        status inference_ingestion_run_status NOT NULL,
        created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
        updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
        PRIMARY KEY (id),
        FOREIGN KEY (job_id) REFERENCES inference_ingestion_jobs (id) ON DELETE CASCADE
    );

-- function to update the updated_at column
CREATE OR REPLACE FUNCTION func_ingestion_result_update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

-- trigger to automatically update updated_at on row updates
CREATE TRIGGER trigger_update_inference_ingestion_run_results_updated_at
    BEFORE UPDATE ON inference_ingestion_run_results
    FOR EACH ROW
    EXECUTE FUNCTION func_ingestion_result_update_updated_at();
