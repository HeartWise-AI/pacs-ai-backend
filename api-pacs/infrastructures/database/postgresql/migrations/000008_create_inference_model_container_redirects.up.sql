CREATE TABLE inference_model_container_redirects (
    tenant_id varchar(50) NOT NULL,
    from_container_id varchar(255) NOT NULL,
    to_container_id varchar(255) NOT NULL,
    model_version varchar(255) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, from_container_id),
    CHECK (from_container_id <> to_container_id)
);

CREATE TRIGGER trigger_update_inference_model_container_redirects_updated_at
    BEFORE UPDATE ON inference_model_container_redirects
    FOR EACH ROW
    EXECUTE FUNCTION func_update_updated_at();
