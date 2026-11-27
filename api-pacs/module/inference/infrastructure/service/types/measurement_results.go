package types

import "strings"

// studyServiceMeasurementResults are results the study-service computes itself
// from the study (no model, no registration, no container): measurements read
// from the DICOM SR. Any user who can read the study may read them, whatever
// the model access of their role.
var studyServiceMeasurementResults = map[string]struct{}{
	"echo-sr-measurements": {},
}

// IsStudyServiceMeasurementResult reports whether a study-service model name is
// a measurement result rather than an AI model.
func IsStudyServiceMeasurementResult(modelName string) bool {
	_, ok := studyServiceMeasurementResults[strings.TrimSpace(modelName)]
	return ok
}
