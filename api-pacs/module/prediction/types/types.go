package types

type DicomInputData struct {
	DicomFilePath string
	Age           int
}

type DicomPrediction struct {
	DetectedVessel string
	LVEF           float64
	Age            int
}
