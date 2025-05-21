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
if ! ollama list | grep -q "llama4"; then
    echo "Pulling llama4 model..."
    ollama pull llama4
    echo "Model llama4 pulled successfully"
fi

# Rather than killing the server and restarting, serve it in client mode
echo "Running llama4 model in the background..."
# Use the model without stopping the server
ollama run llama4 --nowordwrap --verbose 2>&1 &
CLIENT_PID=$!

# Wait for any process to exit
wait $SERVER_PID $CLIENT_PID 