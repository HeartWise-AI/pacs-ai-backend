package service

import (
	"api-pacs/module/prediction/infrastructure/repository"
	"api-pacs/module/prediction/types"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/elastic/go-elasticsearch/v7"
	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/frame"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// Constants
const (
	CERTS_PATH        = "elastic_data/certs/ca/ca.crt"
	ELASTICSEARCH_URL = "https://localhost:9200"
	SERVER_X3D_1_URL  = "http://localhost:8080/predictions/X3D_1" // View detection
	SERVER_X3D_2_URL  = "http://localhost:8080/predictions/X3D_2" // LVEF detection
)

// DEVICE_TYPES and VESSEL_TYPES
var DEVICE_TYPES = []string{"BIO_ICD", "BIO_PM", "BSC_CRT-P", "BSC_ICD", "BSC_PM", "BSC_S-ICD", "ELA_PM", "IMC_PM", "MED_CRT-P",
	"MED_ICD", "MED_ICM", "MED_PM", "SJM_CRT-P", "SJM_ICD", "SJM_PM", "TPS_PM", "VIT_PM"}

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

// Elasticsearch client
func NewElasticsearchClient() (*elasticsearch.Client, error) {
	cfg := elasticsearch.Config{
		Addresses: []string{
			ELASTICSEARCH_URL,
		},
		CACert:   []byte(CERTS_PATH),
		Username: "elastic",
		Password: "MagicWord",
	}
	es, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return es, nil
}

// Read DICOM file
func readDicomFile(filePath string) (*dicom.Dataset, error) {
	ds, err := dicom.ParseFile(filePath, nil)
	if err != nil {
		return nil, err
	}
	return &ds, nil
}

// Send server request
func sendServerRequest(serverURL string, instances []map[string]interface{}) (map[string]interface{}, error) {
	predictRequest, err := json.Marshal(map[string]interface{}{"instances": instances})
	if err != nil {
		return nil, err
	}
	resp, err := http.Post(serverURL, "application/json", bytes.NewBuffer(predictRequest))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

const (
	// Hardcoded DICOM URL
	HARDCODED_DICOM_URL = "http://localhost:8063/instances/3d0069ae-97a2251d-5433aa87-8c60feb9-f8900eaf/file"
)

// PredictionCommandServiceInterface interface
type PredictionCommandServiceInterface interface {
	CreatePrediction(ctx context.Context, data types.DicomInputData) (types.DicomPrediction, error)
}

// PredictionCommandService struct
type PredictionCommandService struct {
	PredictionCommandRepositoryInterface repository.PredictionCommandRepositoryInterface
}

func (s *PredictionCommandService) CreatePrediction(ctx context.Context, data types.DicomInputData) (types.DicomPrediction, error) {
	// Fetch DICOM file from the hardcoded URL
	resp, err := http.Get(HARDCODED_DICOM_URL)
	if err != nil {
		return types.DicomPrediction{}, err
	}
	defer resp.Body.Close()

	// Read the response body
	dicomData, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return types.DicomPrediction{}, err
	}

	// Create a temporary file to store the DICOM data
	tempFile, err := ioutil.TempFile("", "dicom-*.dcm")
	if err != nil {
		return types.DicomPrediction{}, err
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write the DICOM data to the temporary file
	if _, err := tempFile.Write(dicomData); err != nil {
		return types.DicomPrediction{}, err
	}

	// Now use the temp file path in your existing logic
	dataset, err := readDicomFile(tempFile.Name())
	if err != nil {
		return types.DicomPrediction{}, err
	}

	// Detect vessel
	vesselResult, err := detectVesselFromDcm(dataset)
	if err != nil {
		return types.DicomPrediction{}, err
	}

	// Detect LVEF if the vessel is Left Coronary
	var lvef float64
	if vesselResult.DetectedVessel == VESSEL_TYPES[5] {
		lvefResult, err := detectLVEFFromDcm(dataset)
		if err != nil {
			return types.DicomPrediction{}, err
		}
		lvef = (lvefResult.LVEF * 100)
	}

	prediction := types.DicomPrediction{
		DetectedVessel: vesselResult.DetectedVessel,
		LVEF:           lvef,
		Age:            data.Age,
	}

	return prediction, nil
}

// Helper function to convert DICOM to instances
func dcmToInstances(dataset *dicom.Dataset) ([]map[string]interface{}, error) {
	pixelDataElement, err := dataset.FindElementByTag(tag.PixelData)
	if err != nil {
		return nil, fmt.Errorf("error getting pixel data: %v", err)
	}
	frames := pixelDataElement.Value.GetValue().(dicom.PixelDataInfo).Frames
	return convertFramesToBase64Maps(frames)
}

// Temporary
func writeToFile(filename string, content interface{}) error {
	// Open the file, creating it if it doesn't exist
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("error creating file: %v", err)
	}
	defer file.Close()

	// Write the content to the file
	_, err = fmt.Fprintf(file, "%v", content)
	if err != nil {
		return fmt.Errorf("error writing to file: %v", err)
	}

	return nil
}

// base64ImageCathef function (simplified version without video processing)
func convertFramesToBase64Maps(frames []*frame.Frame) ([]map[string]interface{}, error) {
	result := make([]map[string]interface{}, len(frames))

	for i, frame := range frames {
		// Convert 2D int slice to byte slice
		buf := new(bytes.Buffer)
		for _, row := range frame.NativeData.Data {
			for _, val := range row {
				err := binary.Write(buf, binary.LittleEndian, int32(val))
				if err != nil {
					return nil, fmt.Errorf("error writing to buffer: %v", err)
				}
			}
		}

		// Encode to base64
		base64Data := base64.StdEncoding.EncodeToString(buf.Bytes())

		// Create a map for each frame
		frameMap := map[string]interface{}{
			"id":   i,
			"data": base64Data,
		}

		result[i] = frameMap
	}

	err := writeToFile("output.txt", result[0]["data"].(string))
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	return result, nil
}

// detectVesselFromDcm function
func detectVesselFromDcm(dataset *dicom.Dataset) (types.DicomPrediction, error) {
	instances, err := dcmToInstances(dataset)
	if err != nil {
		return types.DicomPrediction{}, err
	}

	result, err := sendServerRequest(SERVER_X3D_1_URL, instances)
	if err != nil {
		return types.DicomPrediction{}, err
	}

	predictions, ok := result["predictions"].([]interface{})
	if !ok || len(predictions) == 0 {
		return types.DicomPrediction{}, errors.New("unexpected prediction result format")
	}

	vesselIndex := 0
	maxProb := 0.0
	for j, prob := range predictions[0].([]interface{}) {
		p, ok := prob.(float64)
		if !ok {
			return types.DicomPrediction{}, errors.New("unexpected probability format")
		}
		if p > maxProb {
			maxProb = p
			vesselIndex = j
		}
	}

	return types.DicomPrediction{
		DetectedVessel: VESSEL_TYPES[vesselIndex],
	}, nil
}

// detectLVEFFromDcm function
func detectLVEFFromDcm(dataset *dicom.Dataset) (types.DicomPrediction, error) {
	instances, err := dcmToInstances(dataset)
	if err != nil {
		return types.DicomPrediction{}, err
	}

	result, err := sendServerRequest(SERVER_X3D_2_URL, instances)
	if err != nil {
		return types.DicomPrediction{}, err
	}

	predictions, ok := result["predictions"].([]interface{})
	if !ok || len(predictions) == 0 {
		return types.DicomPrediction{}, errors.New("unexpected prediction result format")
	}

	lvef, ok := predictions[0].(float64)
	if !ok {
		return types.DicomPrediction{}, errors.New("unexpected LVEF format")
	}

	return types.DicomPrediction{
		LVEF: lvef,
	}, nil
}
