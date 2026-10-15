package cors

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExposedHeadersIncludesRetryAfter(t *testing.T) {
	headers := (&Config{}).ExposedHeaders()

	require.Contains(t, headers, "Retry-After")
}

func TestAllowedHeadersIncludesSSEReconnectCursor(t *testing.T) {
	headers := (&Config{}).AllowedHeaders()

	require.Contains(t, headers, "Last-Event-ID")
}
