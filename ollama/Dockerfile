FROM ollama/ollama:0.7.0

# Copy our startup script
COPY start.sh /start.sh

# Ensure proper line endings and make executable
RUN sed -i 's/\r$//' /start.sh && \
    chmod +x /start.sh

# Set the script as the entrypoint using shell form
ENTRYPOINT ["/bin/bash", "/start.sh"] 