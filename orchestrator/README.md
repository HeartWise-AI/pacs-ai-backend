docker build -t orchestrator .
docker run --network pacs-net -d -p 8585:8585 orchestrator
