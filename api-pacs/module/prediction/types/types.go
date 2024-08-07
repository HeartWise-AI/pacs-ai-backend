package types

type DicomPrediction struct {
	DetectedVessel string
	LVEF           float64
	Age            string
}

var VESSEL_TYPES = map[int]string{
	0:  "Aorta",
	1:  "Catheter",
	2:  "Femoral",
	3:  "Graft",
	4:  "LV",
	5:  "Left Coronary",
	6:  "Other",
	7:  "Pigtail",
	8:  "Radial",
	9:  "Right Coronary",
	10: "Stenting",
}
