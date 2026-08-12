package rest

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewHTTPServerUsesSafeTimeoutDefaults(t *testing.T) {
	t.Setenv("HTTP_SERVER_READ_HEADER_TIMEOUT_SECONDS", "")
	t.Setenv("HTTP_SERVER_READ_TIMEOUT_SECONDS", "invalid")
	t.Setenv("HTTP_SERVER_IDLE_TIMEOUT_SECONDS", "0")

	server := newHTTPServer(8000, http.NotFoundHandler())

	require.Equal(t, ":8000", server.Addr)
	require.Equal(t, defaultReadHeaderTimeout, server.ReadHeaderTimeout)
	require.Equal(t, defaultReadTimeout, server.ReadTimeout)
	require.Equal(t, defaultIdleTimeout, server.IdleTimeout)
	require.Zero(t, server.WriteTimeout)
}

func TestNewHTTPServerUsesConfiguredTimeouts(t *testing.T) {
	t.Setenv("HTTP_SERVER_READ_HEADER_TIMEOUT_SECONDS", "7")
	t.Setenv("HTTP_SERVER_READ_TIMEOUT_SECONDS", "45")
	t.Setenv("HTTP_SERVER_IDLE_TIMEOUT_SECONDS", "90")

	server := newHTTPServer(9000, http.NotFoundHandler())

	require.Equal(t, 7*time.Second, server.ReadHeaderTimeout)
	require.Equal(t, 45*time.Second, server.ReadTimeout)
	require.Equal(t, 90*time.Second, server.IdleTimeout)
}
