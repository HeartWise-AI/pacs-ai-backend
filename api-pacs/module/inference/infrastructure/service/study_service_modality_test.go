package service

import "testing"

func TestCanonicalStudyServiceModality(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "empty", input: "  ", expected: ""},
		{name: "ultrasound DICOM code", input: "US", expected: "echocardiogram"},
		{name: "angiogram DICOM code", input: " xa ", expected: "angiogram"},
		{name: "ECG DICOM code", input: "ECG", expected: "ecg"},
		{name: "computed radiography DICOM code", input: "CR", expected: "chest_xray"},
		{name: "digital radiography DICOM code", input: "DX", expected: "chest_xray"},
		{name: "CT DICOM code", input: "CT", expected: "unknown"},
		{name: "MR DICOM code", input: "MR", expected: "unknown"},
		{name: "PT DICOM code", input: "PT", expected: "unknown"},
		{name: "NM DICOM code", input: "NM", expected: "unknown"},
		{name: "OT DICOM code", input: "OT", expected: "unknown"},
		{name: "SR DICOM code", input: "SR", expected: "unknown"},
		{name: "canonical value", input: " Angiogram ", expected: "angiogram"},
		{name: "unrecognized value matches Python fallback", input: "Custom", expected: "custom"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := canonicalStudyServiceModality(testCase.input); actual != testCase.expected {
				t.Fatalf("canonicalStudyServiceModality(%q) = %q, want %q", testCase.input, actual, testCase.expected)
			}
		})
	}
}

func TestCallbackModalityMismatchUsesCanonicalValues(t *testing.T) {
	testCases := []struct {
		name     string
		incoming string
		expected *string
		mismatch bool
	}{
		{name: "missing planned modality remains permissive", incoming: "angiogram", expected: nil, mismatch: false},
		{name: "legacy DICOM plan accepts canonical callback", incoming: "angiogram", expected: nonEmptyStringPointer("XA"), mismatch: false},
		{name: "canonical plan accepts legacy DICOM callback", incoming: "US", expected: nonEmptyStringPointer("echocardiogram"), mismatch: false},
		{name: "genuinely different modalities are rejected", incoming: "echocardiogram", expected: nonEmptyStringPointer("XA"), mismatch: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := callbackModalityMismatch(testCase.incoming, testCase.expected); actual != testCase.mismatch {
				t.Fatalf("callbackModalityMismatch(%q, %#v) = %t, want %t", testCase.incoming, testCase.expected, actual, testCase.mismatch)
			}
		})
	}
}
