package cloudflare

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateTurnstileTokenWithRemoteIPReturnsActionAndHostname(t *testing.T) {
	secret := strings.Join([]string{"test", "-server", "-value"}, "")
	token := strings.Join([]string{"single", "-use", "-value"}, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/siteverify", request.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		require.Equal(t, secret, body["secret"])
		require.Equal(t, token, body["response"])
		require.Equal(t, "203.0.113.25", body["remoteip"])
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true, "action": "login", "hostname": "app.example.com", "error-codes": []string{},
		})
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_TURNSTILE_BASE_URL", server.URL)
	originalClient := Client
	Client = server.Client()
	t.Cleanup(func() { Client = originalClient })
	api := Init(secret)

	response, err := api.ValidateTurnstileTokenWithRemoteIP(t.Context(), token, "203.0.113.25")

	require.NoError(t, err)
	require.True(t, response.Success)
	require.Equal(t, "login", response.Action)
	require.Equal(t, "app.example.com", response.Hostname)
}
