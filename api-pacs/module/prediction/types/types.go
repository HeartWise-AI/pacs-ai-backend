package types

type DicomInputData struct {
	DicomFilePath string
	URL           string `json:"url"`
}

type DicomPrediction struct {
	DetectedVessel string
	LVEF           float64
	Age            string
}
