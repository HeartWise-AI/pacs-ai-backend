#!/bin/bash
set -e

echo "Starting Ollama server..."
# Start the Ollama server
ollama serve &
SERVER_PID=$!

echo "Waiting for server to be ready..."
# Wait for the server to start up
sleep 10

# Check if model exists, otherwise pull it
if ! ollama list | grep -q "qwen3:8b"; then
    echo "Pulling qwen3:8b model..."
    ollama pull qwen3:8b
    echo "Model qwen3:8b pulled successfully"
fi

# Rather than killing the server and restarting, serve it in client mode
echo "Running qwen3:8b model in the background..."
# Use the model without stopping the server
ollama run qwen3:8b --nowordwrap --verbose 2>&1 &
CLIENT_PID=$!

# Wait for any process to exit
wait $SERVER_PID $CLIENT_PID 