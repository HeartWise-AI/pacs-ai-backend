package cors

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExposedHeadersIncludesRetryAfter(t *testing.T) {
	headers := (&Config{}).ExposedHeaders()

	require.Contains(t, headers, "Retry-After")
}
