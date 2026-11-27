package service

import (
	"net/http"
	"strings"
)

// ServiceTokenTransport adds the shared chat service token (CHAT_FEEDBACK_SERVICE_TOKEN)
// as X-Service-Token to every request api-pacs sends to the chat backend.
//
// The chat backend (cardio-agent medical-agent) cannot verify the PACS-AI session
// token when it runs without FIREBASE_PROJECT_ID, so it refuses bearer routes
// (chat, DICOM, progress, votes) unless the request comes from this gateway,
// which has already authenticated the user and names the user and workspace.
type ServiceTokenTransport struct {
	Token string
	Base  http.RoundTripper
}

func (t *ServiceTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	token := strings.TrimSpace(t.Token)
	if token == "" || req.Header.Get("X-Service-Token") != "" {
		return base.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("X-Service-Token", token)
	return base.RoundTrip(clone)
}

// NewOrchestratorClient returns the HTTP client for the chat backend.
// Redirects are followed only on the same host, so the service token never leaves for another one.
func NewOrchestratorClient(token string, timeoutClient *http.Client) *http.Client {
	client := *timeoutClient
	client.Transport = &ServiceTokenTransport{Token: token, Base: timeoutClient.Transport}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || req.URL.Host != via[0].URL.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &client
}
