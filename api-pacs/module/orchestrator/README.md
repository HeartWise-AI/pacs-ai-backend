# Orchestrator Module

The Orchestrator module provides an API for creating threads, sending messages, and uploading DICOM payloads. It leverages the InferenceCommandService's ability to generate DICOM payloads for inference models.

## API Endpoints

The following endpoints are available:

- `POST /v1/orchestrator/threads` - Create a new thread
- `GET /v1/orchestrator/threads/{threadID}` - Get thread information
- `POST /v1/orchestrator/threads/{threadID}/chat` - Send a message to a thread
- `POST /v1/orchestrator/threads/{threadID}/dicom` - Upload a DICOM payload to a thread

All endpoints require authentication via a JWT token in the Authorization header.

## Configuration

The Orchestrator service can be configured using the following environment variables:

- `ORCHESTRATOR_API_URL` - URL for the external orchestrator API (default: "http://localhost:8585")
- `DEFAULT_CONTAINER_ID` - Default container ID to use for inference when not provided in the payload

## Testing

You can test the Orchestrator API using the provided test client:

```bash
# Set the authentication token
export AUTH_TOKEN=your-token-here

# Run the test client
go run test/test_client.go
```

### Command Line Options

The test client supports the following command line options:

```
  -container string
        Container ID for inference (optional)
  -message string
        Message to send (default "What is the cardiac function in this study?")
  -series1 string
        First Series Instance UID (default "1.2.3.4.5.6.7.8")
  -series2 string
        Second Series Instance UID (default "1.2.3.4.5.6.7.9")
  -study string
        Study Instance UID (default "1.2.3.4.5.6.7")
  -token string
        Authentication token
  -url string
        Base URL for the API (default "http://localhost:8080/v1/orchestrator")
```

### Example:

```bash
# Test with custom study and series UIDs
go run test/test_client.go -study "1.2.840.113619.2.5.1762583153.215519.978957063.78" -series1 "1.2.840.113619.2.5.1762583153.215519.978957063.121"

# Test with a specific container ID
go run test/test_client.go -container "your-container-id"
```

## Implementation Details

The Orchestrator service uses the GenerateInferenceModelPredictRequest method from the InferenceCommandService to generate DICOM payloads. This allows reusing the same payload generation logic in different contexts.

The service currently stores threads and messages in memory. In a production environment, you would want to implement a database repository to persist this data.

## Integration with Python Script

This implementation mimics the functionality of the Python script in `orchestrator/test_dicom_api.py` while respecting the Go API structure. 