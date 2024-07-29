package service

import (
	"api-pacs/module/prediction/infrastructure/repository"
	"api-pacs/module/prediction/types"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"reflect"
	"time"

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

type QueryTorchserve struct {
	Instances [][][][]int `json:"instances"`
}

var (
	client *http.Client = &http.Client{Timeout: 5 * time.Minute}
)

// Send server request
func sendServerRequest(serverURL string, instances QueryTorchserve) ([][]float64, error) {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(instances)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, serverURL, buf)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read the response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Convert body bytes to string
	bodyString := string(bodyBytes)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		log.Println("Error:", bodyString)
		return nil, fmt.Errorf("server returned non-2xx status code: %d", resp.StatusCode)
	}

	// Parse the response into a 2D slice of float64
	var result [][]float64
	err = json.Unmarshal(bodyBytes, &result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// PredictionCommandServiceInterface interface
type PredictionCommandServiceInterface interface {
	CreatePrediction(data types.DicomInputData) (types.DicomPrediction, error)
}

// PredictionCommandService struct
type PredictionCommandService struct {
	PredictionCommandRepositoryInterface repository.PredictionCommandRepositoryInterface
}

func (s *PredictionCommandService) CreatePrediction(data types.DicomInputData) (types.DicomPrediction, error) {
	// Fetch and process DICOM file
	dataset, err := s.fetchAndProcessDicom(data.URL)
	if err != nil {
		return types.DicomPrediction{}, fmt.Errorf("failed to fetch and process DICOM: %w", err)
	}

	ageElement, err := dataset.FindElementByTag(tag.PatientAge)
	if err != nil {
		ageElement = nil
	}

	ageString := "0"
	if ageElement == nil {
		ageString = "0"
	} else {
		ageString = ageElement.String()
	}
	log.Printf("Age: %s", ageString)

	// Detect vessel
	vesselResult, err := detectVesselFromDcm(dataset)
	if err != nil {
		return types.DicomPrediction{}, fmt.Errorf("failed to detect vessel: %w", err)
	}

	// Detect LVEF if the vessel is Left Coronary
	var lvef float64
	if vesselResult.DetectedVessel == VESSEL_TYPES[5] {
		lvefResult, err := detectLVEFFromDcm(dataset)
		if err != nil {
			return types.DicomPrediction{}, fmt.Errorf("failed to detect LVEF: %w", err)
		}
		lvef = lvefResult.LVEF
	}

	return types.DicomPrediction{
		DetectedVessel: vesselResult.DetectedVessel,
		LVEF:           lvef,
		Age:            ageString,
	}, nil
}

func (s *PredictionCommandService) fetchAndProcessDicom(url string) (*dicom.Dataset, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch DICOM: %w", err)
	}
	defer resp.Body.Close()

	tempFile, err := ioutil.TempFile("", "dicom-*.dcm")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		return nil, fmt.Errorf("failed to write DICOM data: %w", err)
	}

	dataset, err := readDicomFile(tempFile.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to read DICOM file: %w", err)
	}

	return dataset, nil
}

// Helper function to convert DICOM to instances
func dcmToInstances(dataset *dicom.Dataset) ([][][][]int, error) {
	pixelDataElement, err := dataset.FindElementByTag(tag.PixelData)
	if err != nil {
		return nil, fmt.Errorf("error getting pixel data: %w", err)
	}

	pixelDataInfo, ok := pixelDataElement.Value.GetValue().(dicom.PixelDataInfo)
	if !ok {
		return nil, fmt.Errorf("invalid pixel data type")
	}

	return convertFramesToBase64Maps(pixelDataInfo.Frames)
}

func getArrayDepth(arr interface{}) int {
	value := reflect.ValueOf(arr)
	depth := 0

	for value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
		depth++
		if value.Len() == 0 {
			break
		}
		value = value.Index(0)
	}

	return depth
}

func convertFramesToBase64Maps(frames []*frame.Frame) ([][][][]int, error) {
	result := make([][][][]int, len(frames))
	for i, frame := range frames {
		rows := frame.NativeData.Rows
		cols := frame.NativeData.Cols
		data := frame.NativeData.Data

		// Pre-allocate the entire 3D slice
		depth := getArrayDepth(frame.NativeData.Data)
		if depth == 2 {
			result[i] = make([][][]int, 1)
		} else {
			result[i] = make([][][]int, len(frame.NativeData.Data[0]))
		}
		result[i][0] = make([][]int, rows)
		for row := range result[i][0] {
			result[i][0][row] = make([]int, cols)
		}

		// Fill the data
		for row := 0; row < rows; row++ {
			for col := 0; col < cols; col++ {
				index := row*cols + col
				if index < len(data) {
					result[i][0][row][col] = data[index][0]
				}
			}
		}
	}
	return result, nil
}

func detectVesselFromDcm(dataset *dicom.Dataset) (types.DicomPrediction, error) {
	instances, err := dcmToInstances(dataset)
	if err != nil {
		return types.DicomPrediction{}, fmt.Errorf("failed to convert DCM to instances: %w", err)
	}

	predictions, err := sendServerRequest(SERVER_X3D_1_URL, QueryTorchserve{Instances: instances})
	if err != nil {
		return types.DicomPrediction{}, fmt.Errorf("failed to send server request: %w", err)
	}

	if len(predictions) == 0 {
		return types.DicomPrediction{}, errors.New("unexpected empty prediction result")
	}

	vesselIndex := findMaxProbabilityIndex(predictions)

	return types.DicomPrediction{
		DetectedVessel: VESSEL_TYPES[vesselIndex],
	}, nil
}

func findMaxProbabilityIndex(predictions [][]float64) int {
	maxIndex, maxProb := 0, predictions[0][0]
	for i, pred := range predictions[1:] {
		if pred[0] > maxProb {
			maxProb = pred[0]
			maxIndex = i + 1
		}
	}
	return maxIndex
}

func detectLVEFFromDcm(dataset *dicom.Dataset) (types.DicomPrediction, error) {
	instances, err := dcmToInstances(dataset)
	if err != nil {
		return types.DicomPrediction{}, fmt.Errorf("failed to convert DCM to instances: %w", err)
	}

	predictions, err := sendServerRequest(SERVER_X3D_2_URL, QueryTorchserve{Instances: instances})
	if err != nil {
		return types.DicomPrediction{}, fmt.Errorf("failed to send server request: %w", err)
	}

	if len(predictions) == 0 || len(predictions[0]) == 0 {
		return types.DicomPrediction{}, errors.New("unexpected empty prediction result")
	}

	return types.DicomPrediction{LVEF: predictions[0][0]}, nil
}
