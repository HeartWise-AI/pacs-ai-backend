package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi"
	"github.com/go-playground/validator/v10"
	"github.com/joho/godotenv"
)

type predictRequest struct {
	Inferences interface{} `json:"inferences"` // can be 5-dimensional slice
	Age        uint        `json:"age"`
	Gender     string      `json:"gender"`
	OutputMode string      `json:"outputMode"`
}

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"predictRequest.Inferences": "Inferences is required",
		"predictRequest.Age":        "Age is required",
		"predictRequest.Gender":     "Gender is required",
		"predictRequest.OutputMode": "Output mode is required",
	}
)

func init() {
	// load env
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
}

func main() {
	port := ":8000"

	r := chi.NewRouter()

	// openapi docs
	r.Group(func(r chi.Router) {
		workDir, _ := os.Getwd()
		docsDir := http.Dir(filepath.Join(workDir, "docs"))
		FileServer(r, "/docs", docsDir)
	})

	// inference service
	r.Route("/inference", func(r chi.Router) {
		r.Post("/predict", predict)
		r.Get("/model-info", getModelInfo)
		r.Get("/model-facts", getModelFacts)
	})

	fmt.Println("Server is listening on " + port)
	log.Fatal(http.ListenAndServe(port, r))
}

func predict(w http.ResponseWriter, r *http.Request) {
	var request predictRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		response := HTTPResponseVM{
			Status:  http.StatusBadRequest,
			Success: false,
			Message: "Invalid payload request",
		}

		response.JSON(w)
		return
	}

	// validate request
	err = Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: "INVALID_PAYLOAD",
			}

			response.JSON(w)
			return
		}

		response := HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: "INVALID_PAYLOAD",
		}

		response.JSON(w)
		return
	}

	switch request.OutputMode {
	case "JSON":
		response := HTTPResponseVM{
			Status:  http.StatusOK,
			Success: true,
			Message: "Prediction successful",
			Data: map[string]interface{}{
				"diagnosis": "limit",
				"predictions": map[string]interface{}{
					"Vessel": map[string]interface{}{
						"probability":   56.534433434343,
						"confidence":    "intermediate",
						"presentable":   true,
						"displayResult": "Left Coronary",
					},
					"LVEF": map[string]interface{}{
						"probability":   65.34343433232,
						"confidence":    "low",
						"presentable":   true,
						"displayResult": 42.2,
					},
				},
				"modelRecommendations": map[string]interface{}{
					"en":          "Recommendation for the next model",
					"fr":          "Recommandation pour le prochain modèle",
					"presentable": true,
				},
			},
		}

		response.JSON(w)
	case "OHIF_ANNOTATIONS":
		response := HTTPResponseVM{
			Status:  http.StatusOK,
			Success: true,
			Message: "Prediction successful",
			Data: map[string]interface{}{
				"metadata": map[string]interface{}{
					"key": "value pair",
				},
				"segmentations": [][][]int{},
				"boundingBoxes": [][][]int{},
				"measurements":  [][][]float64{},
			},
		}

		response.JSON(w)
	case "HTML":
		response := HTTPResponseVM{
			Status:  http.StatusOK,
			Success: true,
			Message: "Prediction successful",
			Data: map[string]interface{}{
				"htmlBase64": "base64 encoded html...",
			},
		}

		response.JSON(w)

	case "WEB_APP":
		response := HTTPResponseVM{
			Status:  http.StatusOK,
			Success: true,
			Message: "Prediction successful",
			Data: map[string]interface{}{
				"webappPath":       "/app/viewer",
				"webappDataBase64": "base64 encoded webapp data...",
			},
		}

		response.JSON(w)

	case "PDF":
		response := HTTPResponseVM{
			Status:  http.StatusOK,
			Success: true,
			Message: "Prediction successful",
			Data: map[string]interface{}{
				"pdfBase64": "base64 encoded pdf...",
			},
		}
		response.JSON(w)
	default:
		response := HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Unsupported output mode",
			ErrorCode: "UNSUPPORTED_OUTPUT_MODE",
		}

		response.JSON(w)
	}
}

func getModelInfo(w http.ResponseWriter, r *http.Request) {
	rootPath, _ := os.Getwd()
	dataPath := filepath.Join(rootPath, "data")

	modelInfo, err := os.ReadFile(filepath.Join(dataPath, "model_info.json"))
	if err != nil {
		response := HTTPResponseVM{
			Status:    http.StatusInternalServerError,
			Success:   false,
			Message:   "Failed to read model info",
			ErrorCode: "MODEL_ERROR",
		}

		response.JSON(w)
		return
	}

	var parsedModelInfo map[string]interface{}
	err = json.Unmarshal(modelInfo, &parsedModelInfo)
	if err != nil {
		response := HTTPResponseVM{
			Status:    http.StatusInternalServerError,
			Success:   false,
			Message:   "Failed to read model info",
			ErrorCode: "MODEL_ERROR",
		}

		response.JSON(w)
		return
	}

	response := HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Model info retrieved successfully",
		Data:    parsedModelInfo,
	}

	response.JSON(w)
}

func getModelFacts(w http.ResponseWriter, r *http.Request) {
	rootPath, _ := os.Getwd()
	dataPath := filepath.Join(rootPath, "data")

	modelFacts, err := os.ReadFile(filepath.Join(dataPath, "model_facts.json"))
	if err != nil {
		response := HTTPResponseVM{
			Status:    http.StatusInternalServerError,
			Success:   false,
			Message:   "Failed to read model facts",
			ErrorCode: "MODEL_ERROR",
		}

		response.JSON(w)
		return
	}

	var parsedModelFacts map[string]interface{}
	err = json.Unmarshal(modelFacts, &parsedModelFacts)
	if err != nil {
		response := HTTPResponseVM{
			Status:    http.StatusInternalServerError,
			Success:   false,
			Message:   "Failed to read model facts",
			ErrorCode: "MODEL_ERROR",
		}

		response.JSON(w)
		return
	}

	response := HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Model facts retrieved successfully",
		Data:    parsedModelFacts,
	}

	response.JSON(w)
}
