package mailchimp

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	types "api-pacs/infrastructures/providers/api/mailchimp/types"
	apiError "api-pacs/internal/errors"
)

type MailchimpAPI struct {
	BaseURL string
	ListID  string
	APIKey  string
}

type status string

const (
	SubscribedStatus status = "subscribed"
)

var (
	client *http.Client = &http.Client{Timeout: 5 * time.Minute}
)

// Init initializes the mailchimp api
func Init(config types.Config) *MailchimpAPI {
	return &MailchimpAPI{
		ListID:  config.ListID,
		APIKey:  config.APIKey,
		BaseURL: config.BaseURL,
	}
}

// Subscribe subscribes an email to the list
func (m *MailchimpAPI) Subscribe(ctx context.Context, email string) error {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(map[string]interface{}{
		"email_address": email,
		"status":        SubscribedStatus,
		"tags":          []string{"newsletter"},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/lists/%s/members", m.BaseURL, m.ListID), buf)
	if err != nil {
		return err
	}

	// set headers
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("anystring", m.APIKey)

	// request with context
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		log.Printf("Error: %v", err)
		return errors.New(apiError.MailchimpAPIError)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error: %v", err)
			return errors.New(apiError.MailchimpAPIError)
		}

		errorMessage := string(response)
		log.Println("Error:", errorMessage)

		return errors.New(apiError.MailchimpAPIError)
	}

	return nil
}

// AddContactForm adds a contact form to the mailchimp list
func (m *MailchimpAPI) AddContactForm(ctx context.Context, request types.AddContactFormRequest) error {
	contact := types.MailchimpContact{
		EmailAddress: request.Email,
		Status:       string(SubscribedStatus),
		MergeFields: types.MergeField{
			Name:    request.Name,
			Message: request.Message,
		},
		Tags: []string{"contact-form"},
	}

	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(contact)
	if err != nil {
		return err
	}

	// use PUT instead of POST - it will update if email exists or create if doesn't
	memberHash := fmt.Sprintf("%x", md5.Sum([]byte(strings.ToLower(request.Email))))

	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/lists/%s/members/%s", m.BaseURL, m.ListID, memberHash), buf)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("anystring", m.APIKey)

	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		errorMessage := string(response)
		log.Println("Error:", errorMessage)

		return errors.New(apiError.MailchimpAPIError)
	}

	return nil
}
