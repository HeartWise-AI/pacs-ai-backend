package inputlimits

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidDICOMUID(t *testing.T) {
	for _, value := range []string{"1", "1.2.840.10008.1.2.1", strings.Repeat("1", 64)} {
		require.True(t, ValidDICOMUID(value), value)
	}
	for _, value := range []string{"", ".1.2", "1.2.", "1..2", "1.2.a", strings.Repeat("1", 65)} {
		require.False(t, ValidDICOMUID(value), value)
	}
}

func TestValidateJSONValueBoundsBytesDepthAndEntries(t *testing.T) {
	require.NoError(t, ValidateJSONValue(map[string]interface{}{"view": "A4C"}, 32, 3, 2))
	require.Error(t, ValidateJSONValue(map[string]interface{}{"value": strings.Repeat("x", 40)}, 16, 8, 20))
	require.Error(t, ValidateJSONValue(map[string]interface{}{"a": map[string]interface{}{"b": "c"}}, 1024, 2, 20))
	require.Error(t, ValidateJSONValue(map[string]interface{}{"a": 1, "b": 2}, 1024, 8, 1))
}
