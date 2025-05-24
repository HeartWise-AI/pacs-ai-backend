package dicom

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/frame"
	"github.com/suyashkumar/dicom/pkg/tag"
	"github.com/suyashkumar/dicom/pkg/uid"

	apiError "api-pacs/internal/errors"
)

// ConvertBulkDataURIToInlineBinary convert bulk data URI to inline binary
func ConvertBulkDataURIToInlineBinary(bulkDataURI string) (string, error) {
	bulkDataURI = strings.ReplaceAll(bulkDataURI, "/orthanc/", "/") // remove /orthanc/ prefix

	resp, err := http.Get(bulkDataURI)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New(apiError.DICOMParseError)
	}

	// get content type and boundary
	contentType := resp.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		log.Println("[dicom-web] error parsing content type", err)
		return "", errors.New(apiError.DICOMParseError)
	}

	// check if content type is multipart/related
	if strings.HasPrefix(mediaType, "multipart/related") {
		boundary := params["boundary"]
		if len(boundary) == 0 {
			log.Println("[dicom-web] boundary is empty")
			return "", errors.New(apiError.DICOMParseError)
		}

		mr := multipart.NewReader(resp.Body, boundary)
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Println("[dicom-web] error reading part", err)
				return "", errors.New(apiError.DICOMParseError)
			}

			if strings.HasPrefix(part.Header.Get("Content-Type"), "application/octet-stream") {
				data, err := io.ReadAll(part)
				if err != nil {
					log.Println("[dicom-web] error reading image data", err)
					return "", errors.New(apiError.DICOMParseError)
				}

				// encode to base64
				base64Data := base64.StdEncoding.EncodeToString(data)
				return base64Data, nil
			}
		}
	}

	log.Println("[dicom-web] no application/octet-stream part found")
	return "", errors.New(apiError.DICOMParseError)
}

// ConvertPDFToDICOM convert PDF to DICOM
func ConvertPDFToDICOM(pdfData []byte, studyInstanceUID, seriesInstanceUID, sopInstanceUID, seriesDescription, documentTitle string) ([]byte, error) {
	// create DICOM elements
	elements := []*dicom.Element{
		// required identifiers
		mustNewElement(tag.MediaStorageSOPClassUID, []string{"1.2.840.10008.5.1.4.1.1.104.1"}), // Encapsulated PDF Storage
		mustNewElement(tag.MediaStorageSOPInstanceUID, []string{sopInstanceUID}),
		mustNewElement(tag.TransferSyntaxUID, []string{uid.ExplicitVRLittleEndian}),

		// study/series relationship
		mustNewElement(tag.StudyInstanceUID, []string{studyInstanceUID}),
		mustNewElement(tag.SeriesInstanceUID, []string{seriesInstanceUID}),
		mustNewElement(tag.SeriesDescription, []string{seriesDescription}),

		// pdf content
		mustNewElement(tag.Modality, []string{"DOC"}), // Document Modality
		mustNewElement(tag.DocumentTitle, []string{documentTitle}),
		mustNewElement(tag.EncapsulatedDocument, pdfData),
		mustNewElement(tag.MIMETypeOfEncapsulatedDocument, []string{"application/pdf"}),

		// recommended metadata
		mustNewElement(tag.InstanceNumber, []int{1}),
		mustNewElement(tag.ContentDate, []string{time.Now().Format("20060102")}),
		mustNewElement(tag.ContentTime, []string{time.Now().Format("150405")}),
	}

	// create dataset
	dataset := dicom.Dataset{Elements: elements}

	// write to buffer
	var buf bytes.Buffer
	if err := dicom.Write(&buf, dataset, nil); err != nil {
		log.Println("[dicom] error converting PDF to DICOM:", err)
		return nil, errors.New(apiError.DICOMParseError)
	}

	return buf.Bytes(), nil
}

// DICOMToInstances convert DICOM to instances
func DICOMToInstances(dataset dicom.Dataset) ([][][][]int, error) {
	pixelDataElement, err := dataset.FindElementByTag(tag.PixelData)
	if err != nil {
		return nil, fmt.Errorf("error getting pixel data: %w", err)
	}

	pixelDataInfo, ok := pixelDataElement.Value.GetValue().(dicom.PixelDataInfo)
	if !ok {
		return nil, fmt.Errorf("invalid pixel data type")
	}

	return convertFramesToBase64Maps(pixelDataInfo.Frames), nil
}

// FindMaxProbabilityIndex find max probability index
func FindMaxProbabilityIndex(dataset [][]float64) int {
	maxIndex, maxProb := 0, dataset[0][0]
	for i, pred := range dataset[1:] {
		if pred[0] > maxProb {
			maxProb = pred[0]
			maxIndex = i + 1
		}
	}

	return maxIndex
}

// ParseAge parse age from DICOM dataset
func ParseAge(dataset dicom.Dataset) (int, error) {
	ageElement, _ := dataset.FindElementByTag(tag.PatientBirthDate)
	birthDateStr := ageElement.Value.String()
	age, err := calculateAge(birthDateStr)
	if err != nil {
		return 0, err
	}

	return age, nil
}

// ParseGender parse gender from DICOM dataset
func ParseGender(dataset dicom.Dataset) (string, error) {
	genderElement, err := dataset.FindElementByTag(tag.PatientSex)
	if err != nil {
		return "", fmt.Errorf("error finding patient sex: %w", err)
	}

	gender := strings.Trim(genderElement.Value.String(), "[] \t\n\r")

	// DICOM standard defines these values:
	// M = male
	// F = female
	// O = other
	switch gender {
	case "M":
		return "MALE", nil
	case "F":
		return "FEMALE", nil
	case "O":
		return "OTHER", nil
	default:
		log.Println("GENDER:", genderElement.Value.String(), gender)
		return "UNKNOWN", nil
	}
}

func calculateAge(birthDateStr string) (int, error) {
	birthDateStr = strings.Trim(birthDateStr, "[] \t\n\r")
	birthDate, err := time.Parse("20060102", birthDateStr)
	if err != nil {
		log.Println(err)
		return 0, err
	}

	now := time.Now()
	age := now.Year() - birthDate.Year()

	// adjust age if birthday hasn't occurred this year
	if now.YearDay() < birthDate.YearDay() {
		age--
	}

	return age, nil
}

func convertFramesToBase64Maps(frames []*frame.Frame) [][][][]int {
	result := make([][][][]int, len(frames))

	for i, frame := range frames {
		rows := frame.NativeData.Rows
		cols := frame.NativeData.Cols
		data := frame.NativeData.Data

		// pre-allocate the entire 3D slice
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

		// fill the data
		for row := 0; row < rows; row++ {
			for col := 0; col < cols; col++ {
				index := row*cols + col
				if index < len(data) {
					result[i][0][row][col] = data[index][0]
				}
			}
		}
	}

	return result
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

func mustNewElement(t tag.Tag, data any) *dicom.Element {
	elem, err := dicom.NewElement(t, data)
	if err != nil {
		log.Panic(err)
	}

	return elem
}
