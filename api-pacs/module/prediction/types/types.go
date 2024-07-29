package types

type DicomInputData struct {
	UID string `json:"uid"`
}

type DicomPrediction struct {
	DetectedVessel string
	LVEF           float64
	Age            string
}
