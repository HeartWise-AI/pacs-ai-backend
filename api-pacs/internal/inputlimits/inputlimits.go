package inputlimits

import (
	"encoding/json"
	"errors"
)

const maxDICOMUIDLength = 64

var ErrJSONValueTooLarge = errors.New("JSON value exceeds configured limits")

func ValidDICOMUID(value string) bool {
	if value == "" || len(value) > maxDICOMUIDLength || value[0] == '.' || value[len(value)-1] == '.' {
		return false
	}
	previousDot := false
	for _, character := range value {
		if character == '.' {
			if previousDot {
				return false
			}
			previousDot = true
			continue
		}
		if character < '0' || character > '9' {
			return false
		}
		previousDot = false
	}
	return true
}

func ValidateJSONValue(value interface{}, maxBytes, maxDepth, maxEntries int) error {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxBytes {
		return ErrJSONValueTooLarge
	}
	entries := 0
	if !boundedJSONValue(value, 1, maxDepth, maxEntries, &entries) {
		return ErrJSONValueTooLarge
	}
	return nil
}

func boundedJSONValue(value interface{}, depth, maxDepth, maxEntries int, entries *int) bool {
	if depth > maxDepth {
		return false
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		*entries += len(typed)
		if *entries > maxEntries {
			return false
		}
		for _, nested := range typed {
			if !boundedJSONValue(nested, depth+1, maxDepth, maxEntries, entries) {
				return false
			}
		}
	case []interface{}:
		*entries += len(typed)
		if *entries > maxEntries {
			return false
		}
		for _, nested := range typed {
			if !boundedJSONValue(nested, depth+1, maxDepth, maxEntries, entries) {
				return false
			}
		}
	}
	return true
}
