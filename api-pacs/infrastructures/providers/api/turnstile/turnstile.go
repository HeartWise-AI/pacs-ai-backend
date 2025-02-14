package turnstile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"api-pacs/infrastructures/providers/api/turnstile/types"
	apiError "api-pacs/internal/errors"
)

type TurnstileAPI struct {
	BaseURL   string
	SecretKey string
}

var (
	client *http.Client = &http.Client{Timeout: 5 * time.Minute}
)

// Init initializes the turnstile api
func Init(baseURL string, secretKey string) *TurnstileAPI {
	return &TurnstileAPI{
		BaseURL:   baseURL,
		SecretKey: secretKey,
	}
}

// ValidateTurnstileToken validates the turnstile token
func (t *TurnstileAPI) ValidateTurnstileToken(ctx context.Context, token string) (types.ValidateTurnstileTokenResponse, error) {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(map[string]interface{}{
		"secret":   t.SecretKey,
		"response": token,
	})
	if err != nil {
		return types.ValidateTurnstileTokenResponse{}, err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/siteverify", t.BaseURL), buf)
	if err != nil {
		return types.ValidateTurnstileTokenResponse{}, err
	}

	// set headers
	req.Header.Set("Content-Type", "application/json")

	// request with context
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return types.ValidateTurnstileTokenResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error: %v", err)
			return types.ValidateTurnstileTokenResponse{}, errors.New(apiError.TurnstileAPIError)
		}

		errorMessage := string(response)
		log.Println("Error:", errorMessage)

		return types.ValidateTurnstileTokenResponse{}, errors.New(apiError.TurnstileAPIError)
	}

	var response types.ValidateTurnstileTokenResponse
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		log.Printf("Error: %v", err)
		return types.ValidateTurnstileTokenResponse{}, errors.New(apiError.TurnstileAPIError)
	}
	return response, nil
}
