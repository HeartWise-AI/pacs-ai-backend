package service

import (
	"api-pacs/module/prediction/infrastructure/repository"
	"api-pacs/module/prediction/infrastructure/service/types"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/frame"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// Constants
const (
	SERVER_X3D_1_URL = "http://localhost:8080/predictions/X3D_1" // View detection // change to "http://torhcserve:8080/predictions/X3D_1"
	SERVER_X3D_2_URL = "http://localhost:8080/predictions/X3D_2" // LVEF detection // change to "http://torhcserve:8080/predictions/X3D_2"
)

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
// type PredictionCommandServiceInterface interface {
// 	CreatePrediction(data string) (types.DicomPrediction, error)
// }

// PredictionCommandService struct
type PredictionCommandService struct {
	repository.PredictionCommandRepositoryInterface
}

func calculateAgeString(birthDateStr string) string {
	birthDateStr = strings.Trim(birthDateStr, "[] \t\n\r")

	birthDate, err := time.Parse("20060102", birthDateStr)
	if err != nil {
		log.Printf("Error parsing birth date: %v", err)
		return "Unknown"
	}

	now := time.Now()
	age := now.Year() - birthDate.Year()

	// Adjust age if birthday hasn't occurred this year
	if now.YearDay() < birthDate.YearDay() {
		age--
	}

	return strconv.Itoa(age)
}

func (s *PredictionCommandService) CreatePrediction(data string) (types.DicomPrediction, error) {
	// Fetch and process DICOM file
	dataset, err := s.fetchAndProcessDicom(data)
	if err != nil {
		return types.DicomPrediction{}, fmt.Errorf("failed to fetch and process DICOM: %w", err)
	}

	ageElement, _ := dataset.FindElementByTag(tag.PatientBirthDate)
	birthDateStr := ageElement.Value.String()

	ageString := calculateAgeString(birthDateStr)
	// Detect vessel
	vesselResult, err := detectVesselFromDcm(dataset)
	if err != nil {
		return types.DicomPrediction{}, fmt.Errorf("failed to detect vessel: %w", err)
	}

	// Detect LVEF if the vessel is Left Coronary
	var lvef float64
	if vesselResult.DetectedVessel == types.VESSEL_TYPES[5] {
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

func (s *PredictionCommandService) fetchDicomDataByID(uid string) ([]byte, error) {
	// Fetch the DICOM data
	link := "http://localhost:8053/instances/" + uid + "/file" // change to "http://orthanc:8042/instances/"
	resp, err := http.Get(link)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch DICOM: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return bodyBytes, nil
}

func (s *PredictionCommandService) fetchAndProcessDicom(queryID string) (*dicom.Dataset, error) {
	// Use the queryID to fetch the DICOM data
	dicomData, err := s.fetchDicomDataByID(queryID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch DICOM data: %w", err)
	}

	// Process the DICOM data
	dataset, err := dicom.Parse(bytes.NewReader(dicomData), int64(len(dicomData)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DICOM data: %w", err)
	}

	return &dataset, nil
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
		DetectedVessel: types.VESSEL_TYPES[vesselIndex],
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
