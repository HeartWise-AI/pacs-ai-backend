package service

import "strings"

var dicomToStudyServiceModality = map[string]string{
	"US":  "echocardiogram",
	"XA":  "angiogram",
	"ECG": "ecg",
	"CR":  "chest_xray",
	"DX":  "chest_xray",
	"CT":  "unknown",
	"MR":  "unknown",
	"PT":  "unknown",
	"NM":  "unknown",
	"OT":  "unknown",
	"SR":  "unknown",
}

var studyServiceModalities = map[string]struct{}{
	"echocardiogram": {},
	"angiogram":      {},
	"ecg":            {},
	"chest_xray":     {},
	"unknown":        {},
}

// canonicalStudyServiceModality mirrors study-service ingest normalization so
// both services persist and compare the same modality vocabulary.
func canonicalStudyServiceModality(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	lowered := strings.ToLower(trimmed)
	if _, ok := studyServiceModalities[lowered]; ok {
		return lowered
	}
	if mapped, ok := dicomToStudyServiceModality[strings.ToUpper(trimmed)]; ok {
		return mapped
	}

	return lowered
}

func callbackModalityMismatch(incoming string, expected *string) bool {
	if expected == nil || strings.TrimSpace(*expected) == "" {
		return false
	}

	return canonicalStudyServiceModality(incoming) != canonicalStudyServiceModality(*expected)
}
