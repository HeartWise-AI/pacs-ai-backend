package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"api-pacs/infrastructures/providers/api/cloudflare/types"
	apiError "api-pacs/internal/errors"
)

// ValidateTurnstileToken validates the turnstile token
func (t *CloudflareAPI) ValidateTurnstileToken(ctx context.Context, token string) (types.ValidateTurnstileTokenResponse, error) {
	return t.validateTurnstileToken(ctx, token, "")
}

// ValidateTurnstileTokenWithRemoteIP validates a login token and binds the
// provider assessment to the trusted socket-derived client IP.
func (t *CloudflareAPI) ValidateTurnstileTokenWithRemoteIP(ctx context.Context, token, remoteIP string) (types.ValidateTurnstileTokenResponse, error) {
	return t.validateTurnstileToken(ctx, token, remoteIP)
}

func (t *CloudflareAPI) validateTurnstileToken(ctx context.Context, token, remoteIP string) (types.ValidateTurnstileTokenResponse, error) {
	buf := new(bytes.Buffer)
	payload := map[string]interface{}{
		"secret":   t.SecretKey,
		"response": token,
	}
	if remoteIP != "" && remoteIP != "unknown" {
		payload["remoteip"] = remoteIP
	}
	err := json.NewEncoder(buf).Encode(payload)
	if err != nil {
		return types.ValidateTurnstileTokenResponse{}, err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/siteverify", os.Getenv("CLOUDFLARE_TURNSTILE_BASE_URL")), buf)
	if err != nil {
		return types.ValidateTurnstileTokenResponse{}, err
	}

	// set headers
	req.Header.Set("Content-Type", "application/json")

	// request with context
	resp, err := Client.Do(req.WithContext(ctx))
	if err != nil {
		return types.ValidateTurnstileTokenResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		if err != nil {
			return types.ValidateTurnstileTokenResponse{}, errors.New(apiError.CloudflareAPIError)
		}
		return types.ValidateTurnstileTokenResponse{}, errors.New(apiError.CloudflareAPIError)
	}

	var response types.ValidateTurnstileTokenResponse
	err = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&response)
	if err != nil {
		log.Printf("Error: %v", err)
		return types.ValidateTurnstileTokenResponse{}, errors.New(apiError.CloudflareAPIError)
	}

	return response, nil
}
