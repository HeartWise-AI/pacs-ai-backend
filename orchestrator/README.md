# Orchestrator

AI Agent Ochestrator to call medical models and websearch

## Local Development

Setup the .env file first

- `cp .env.example .env`

- Update necessary keys in `.env`

Run:
- `cd pacs-ai-backend/orchestrator`
- `docker compose build` or `docker compose build --no-cache` to force rebuild
- `docker compose up -d`

Check if the application is running:
- `docker compose logs orchestrator -f`