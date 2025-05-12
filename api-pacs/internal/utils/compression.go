package utils

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"strings"
)

// CompressAndEncodeToBase64 compresses data using gzip and then encodes it using base64
// The output format is "gz:" + base64(gzipped data) to indicate it's compressed
func CompressAndEncodeToBase64(data []byte) (string, error) {
	// Create a buffer to hold the compressed data
	var compressedBuf bytes.Buffer

	// Create a gzip writer
	gzipWriter := gzip.NewWriter(&compressedBuf)

	// Write the data to the gzip writer
	_, err := gzipWriter.Write(data)
	if err != nil {
		return "", err
	}

	// Close the gzip writer to flush any remaining data
	if err := gzipWriter.Close(); err != nil {
		return "", err
	}

	// Encode the compressed data to base64
	encodedStr := base64.StdEncoding.EncodeToString(compressedBuf.Bytes())

	// Prefix with "gz:" to indicate this is gzipped data
	return "gz:" + encodedStr, nil
}

// DecodeAndDecompressFromBase64 decodes base64 string and decompresses if needed
// Handles both regular base64 and compressed+encoded data (prefixed with "gz:")
func DecodeAndDecompressFromBase64(encodedStr string) ([]byte, error) {
	// Check if the string is compressed
	if strings.HasPrefix(encodedStr, "gz:") {
		// Strip the prefix
		encodedStr = strings.TrimPrefix(encodedStr, "gz:")

		// Decode base64
		compressedData, err := base64.StdEncoding.DecodeString(encodedStr)
		if err != nil {
			return nil, err
		}

		// Create a reader for the compressed data
		gzipReader, err := gzip.NewReader(bytes.NewReader(compressedData))
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()

		// Read and decompress the data
		return io.ReadAll(gzipReader)
	}

	// Regular base64 decode if not compressed
	return base64.StdEncoding.DecodeString(encodedStr)
}
