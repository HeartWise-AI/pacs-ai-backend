package rest

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/go-playground/validator/v10"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	types "api-pacs/module/inference/interfaces/http"
)

const defaultManagedInferenceMaxRequestBodyBytes int64 = 2 * 1024 * 1024 * 1024

// PredictPreparedInferenceModel is the authenticated study-service gateway for
// already prepared DICOM payloads. The service resolves and authorizes the
// container reference before it reaches the model manager.
func (controller *InferenceCommandController) PredictPreparedInferenceModel(w http.ResponseWriter, r *http.Request) {
	if !authorizeStudyService(w, r) {
		return
	}

	// The model request stays encoded: it is forwarded untouched (see PredictRequest.Raw).
	var request managedPredictEnvelope
	maxBytes := requestbody.PositiveInt64FromEnvironment(
		"INTERNAL_INFERENCE_PREDICT_MAX_REQUEST_BODY_BYTES",
		defaultManagedInferenceMaxRequestBodyBytes,
	)
	if r.ContentLength > maxBytes {
		requestbody.ObserveRejection(r, maxBytes, "internal_inference")
		requestbody.WriteTooLarge(w)
		return
	}
	if managedPredictStreamingEnabled() && r.ContentLength >= managedFastPathMinBytes {
		// A study payload of known length is forwarded while it arrives. A
		// layout the head parser does not recognise is left unread in the
		// buffered reader and takes the buffered path below.
		limited := http.MaxBytesReader(w, r.Body, maxBytes)
		head := bufio.NewReaderSize(limited, managedHeadPeekBytes)
		if controller.predictPreparedStream(w, r, head, limited, maxBytes) {
			return
		}
		r.Body = struct {
			io.Reader
			io.Closer
		}{head, limited}
	}
	body, err := readManagedBody(w, r, maxBytes)
	if err != nil {
		if requestbody.IsTooLarge(err) {
			requestbody.ObserveRejection(r, maxBytes, "internal_inference")
			requestbody.WriteTooLarge(w)
			return
		}
		writeManagedInferenceResponse(w, http.StatusBadRequest, "Invalid payload request.", apiError.InvalidRequestPayload)
		return
	}
	if fast, ok := splitManagedEnvelope(body); ok {
		request = fast
	} else if err := json.Unmarshal(body, &request); err != nil {
		writeManagedInferenceResponse(w, http.StatusBadRequest, "Invalid payload request.", apiError.InvalidRequestPayload)
		return
	}
	if err := types.Validate.Struct(request); err != nil {
		if _, ok := err.(validator.ValidationErrors); ok {
			writeManagedInferenceResponse(w, http.StatusBadRequest, "Tenant ID and container reference are required.", apiError.InvalidPayload)
			return
		}
		writeManagedInferenceResponse(w, http.StatusBadRequest, "Invalid payload request.", apiError.InvalidRequestPayload)
		return
	}

	if trimmed := bytes.TrimLeft(request.Request, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
		writeManagedInferenceResponse(w, http.StatusBadRequest, "Invalid payload request.", apiError.InvalidRequestPayload)
		return
	}

	result, err := controller.InferenceCommandServiceInterface.PredictPreparedInferenceModel(
		r.Context(), request.TenantID, request.ContainerRef, dockerInferenceTypes.PredictRequest{Raw: request.Request},
	)
	if err != nil {
		writeManagedPredictError(w, err)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully applied prediction to inference model.",
		Data:    result.Data,
	}
	response.JSON(w)
}

func writeManagedPredictError(w http.ResponseWriter, err error) {
	if writeInferenceManagerError(w, err) {
		return
	}
	switch err.Error() {
	case apiError.InvalidPayload:
		writeManagedInferenceResponse(w, http.StatusBadRequest, "Invalid payload request.", err.Error())
	case apiError.MissingRecord:
		writeManagedInferenceResponse(w, http.StatusNotFound, "Inference model is not registered for this tenant.", err.Error())
	case apiError.DockerError, apiError.DockerInferenceError:
		writeManagedInferenceResponse(w, http.StatusInternalServerError, "Inference runtime encountered an error.", err.Error())
	default:
		writeManagedInferenceResponse(w, http.StatusInternalServerError, "Please contact technical support.", err.Error())
	}
}

func authorizeStudyService(w http.ResponseWriter, r *http.Request) bool {
	token := strings.TrimSpace(os.Getenv("STUDY_SERVICE_CALLBACK_TOKEN"))
	if token == "" {
		writeManagedInferenceResponse(w, http.StatusServiceUnavailable, "Study-service gateway auth is not configured.", apiError.MissingConfiguration)
		return false
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, candidate, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(strings.TrimSpace(scheme), "Bearer") ||
		subtle.ConstantTimeCompare([]byte(strings.TrimSpace(candidate)), []byte(token)) != 1 {
		writeManagedInferenceResponse(w, http.StatusUnauthorized, "Invalid Authorization header.", apiError.UnauthorizedAccess)
		return false
	}
	return true
}

func writeManagedInferenceResponse(w http.ResponseWriter, status int, message string, errorCode interface{}) {
	response := viewmodels.HTTPResponseVM{
		Status: status, Success: false, Message: message, ErrorCode: errorCode,
	}
	response.JSON(w)
}

type managedPredictEnvelope struct {
	TenantID     string          `json:"tenantId" validate:"required"`
	ContainerRef string          `json:"containerRef" validate:"required"`
	Request      json.RawMessage `json:"request" validate:"required"`
}

// managedFastPathMinBytes is the body size from which the envelope is split
// without parsing the model request. Smaller bodies are fully validated.
const managedFastPathMinBytes = 8 << 20

func readManagedBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	limited := http.MaxBytesReader(w, r.Body, maxBytes)
	capacity := int64(bytes.MinRead)
	if r.ContentLength > 0 && r.ContentLength <= maxBytes {
		capacity = r.ContentLength + bytes.MinRead
	}
	buffer := bytes.NewBuffer(make([]byte, 0, capacity))
	if _, err := buffer.ReadFrom(limited); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, requestbody.ErrTooLarge
		}
		return nil, err
	}
	return buffer.Bytes(), nil
}

// splitManagedEnvelope reads tenantId and containerRef with a token decoder
// and returns the bytes of "request" without scanning them. A study payload is
// gigabytes of base64; validating it here only repeats what the model does and
// took most of the gateway's time. It applies to large bodies whose last key
// is "request", which is how study-service writes them; anything else returns
// false and is decoded in full.
func splitManagedEnvelope(body []byte) (managedPredictEnvelope, bool) {
	var envelope managedPredictEnvelope
	if len(body) < managedFastPathMinBytes {
		return envelope, false
	}
	envelope, offset, ok := parseManagedHead(body)
	if !ok {
		return envelope, false
	}
	rest := bytes.TrimRight(body[offset:], " \t\r\n")
	// rest is now `<request value>}`: drop the envelope's closing brace.
	if len(rest) < 3 || rest[len(rest)-1] != '}' {
		return envelope, false
	}
	value := bytes.TrimRight(rest[:len(rest)-1], " \t\r\n")
	if len(value) < 2 || value[0] != '{' || value[len(value)-1] != '}' {
		return envelope, false
	}
	envelope.Request = value
	return envelope, true
}

// parseManagedHead reads the envelope up to its "request" key and returns the
// tenantId and containerRef found before it and the offset of the first byte
// of the request value, an opening brace. head may be only the start of the
// body: whatever cannot be decided from it returns false.
func parseManagedHead(head []byte) (managedPredictEnvelope, int, bool) {
	var envelope managedPredictEnvelope
	decoder := json.NewDecoder(bytes.NewReader(head))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return envelope, 0, false
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			return envelope, 0, false
		}
		key, ok := token.(string)
		if !ok {
			return envelope, 0, false
		}
		switch key {
		case "tenantId":
			if decoder.Decode(&envelope.TenantID) != nil {
				return envelope, 0, false
			}
		case "containerRef":
			if decoder.Decode(&envelope.ContainerRef) != nil {
				return envelope, 0, false
			}
		case "request":
			offset := int(decoder.InputOffset())
			offset += len(head[offset:]) - len(bytes.TrimLeft(head[offset:], " \t\r\n"))
			if offset >= len(head) || head[offset] != ':' {
				return envelope, 0, false
			}
			offset++
			offset += len(head[offset:]) - len(bytes.TrimLeft(head[offset:], " \t\r\n"))
			if offset >= len(head) || head[offset] != '{' {
				return envelope, 0, false
			}
			return envelope, offset, true
		default:
			return envelope, 0, false
		}
	}
}
