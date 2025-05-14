package utils

import (
	"encoding/base64"
	"strings"

	"github.com/golang/snappy"
)

// CompressAndEncodeToBase64 compresses data using Snappy and then encodes it using base64
// The output format is "snappy:" + base64(snappy compressed data) to indicate it's compressed
func CompressAndEncodeToBase64(data []byte) (string, error) {
	// Compress the data using Snappy
	compressedData := snappy.Encode(nil, data)

	// Encode the compressed data to base64
	encodedStr := base64.StdEncoding.EncodeToString(compressedData)

	// Prefix with "snappy:" to indicate this is Snappy compressed data
	return "snappy:" + encodedStr, nil
}

// DecodeAndDecompressFromBase64 decodes base64 string and decompresses if needed
// Handles both regular base64 and compressed+encoded data (prefixed with "snappy:")
func DecodeAndDecompressFromBase64(encodedStr string) ([]byte, error) {
	// Check if the string is compressed
	if strings.HasPrefix(encodedStr, "snappy:") {
		// Strip the prefix
		encodedStr = strings.TrimPrefix(encodedStr, "snappy:")

		// Decode base64
		compressedData, err := base64.StdEncoding.DecodeString(encodedStr)
		if err != nil {
			return nil, err
		}

		// Decompress the data using Snappy
		decompressedData, err := snappy.Decode(nil, compressedData)
		if err != nil {
			return nil, err
		}

		return decompressedData, nil
	}

	// Regular base64 decode if not compressed
	return base64.StdEncoding.DecodeString(encodedStr)
}
