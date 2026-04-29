# PostgreSQL

## Database Migrations

`api-pacs` uses `go-migrate` to manage PostgreSQL schema changes stored in:

- `api-pacs/infrastructures/database/postgresql/migrations`

For a fresh database startup, create the database container first, then run the migrations before expecting the inference tables to exist.

### Recommended: use `migrate`

Install `migrate` and run the commands from `api-pacs/`.

To create a new migration:

```bash
cd /home/pacs-ai/pacs-ai-backend/api-pacs
NAME=<init_schema> make migrate-schema
```

To migrate up against the running local PostgreSQL container:

```bash
cd /home/pacs-ai/pacs-ai-backend/api-pacs
make migrate-up POSTGRES_DB_HOST=localhost POSTGRES_DB_PORT=5433
```

To migrate down:

```bash
cd /home/pacs-ai/pacs-ai-backend/api-pacs
make migrate-down POSTGRES_DB_HOST=localhost POSTGRES_DB_PORT=5433
```

To check migration version:

```bash
cd /home/pacs-ai/pacs-ai-backend/api-pacs
make migrate-version POSTGRES_DB_HOST=localhost POSTGRES_DB_PORT=5433
```

To force migration version:

```bash
cd /home/pacs-ai/pacs-ai-backend/api-pacs
make migrate-force STEPS=<version> POSTGRES_DB_HOST=localhost POSTGRES_DB_PORT=5433
```

### Fallback: run SQL manually with `psql`

If `migrate` is not installed, you can bootstrap a fresh database manually:

```bash
docker exec -i postgresql psql -U iam_inference_db -d inference_db < /home/pacs-ai/pacs-ai-backend/api-pacs/infrastructures/database/postgresql/migrations/000001_create_inference_ingestion_jobs_schema.up.sql
docker exec -i postgresql psql -U iam_inference_db -d inference_db < /home/pacs-ai/pacs-ai-backend/api-pacs/infrastructures/database/postgresql/migrations/000002_create_inference_ingestion_candidates_schema.up.sql
docker exec -i postgresql psql -U iam_inference_db -d inference_db < /home/pacs-ai/pacs-ai-backend/api-pacs/infrastructures/database/postgresql/migrations/000003_create_inference_ingestion_processing_jobs_schema.up.sql
```

Then verify the tables were created:

```bash
docker exec postgresql psql -U iam_inference_db -d inference_db -c '\dt'
```

Expected tables:

- `inference_ingestion_jobs`
- `inference_ingestion_candidates`
- `inference_ingestion_processing_jobs`
