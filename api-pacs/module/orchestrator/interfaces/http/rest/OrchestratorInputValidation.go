package rest

import (
	"errors"
	"log"
	"net/http"

	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/internal/inputlimits"
	httpTypes "api-pacs/module/orchestrator/interfaces/http"
)

const (
	defaultOrchestratorMaxRequestBodyBytes   int64 = 8 * 1024 * 1024
	defaultOrchestratorMaxStudies                  = 20
	defaultOrchestratorMaxSeriesPerStudy           = 256
	defaultOrchestratorMaxPreviewBase64Bytes       = 2 * 1024 * 1024
	defaultOrchestratorMaxMetadataBytes            = 64 * 1024
	defaultOrchestratorMaxMetadataDepth            = 8
	defaultOrchestratorMaxMetadataEntries          = 256
	defaultOrchestratorMaxMessageBytes             = 32 * 1024
)

var errOrchestratorInputLimit = errors.New(apiError.RequestInputLimitExceeded)

type orchestratorInputLimits struct {
	MaxRequestBodyBytes   int64
	MaxStudies            int
	MaxSeriesPerStudy     int
	MaxPreviewBase64Bytes int
	MaxMetadataBytes      int
	MaxMetadataDepth      int
	MaxMetadataEntries    int
	MaxMessageBytes       int
}

func configuredOrchestratorInputLimits() orchestratorInputLimits {
	return orchestratorInputLimits{
		MaxRequestBodyBytes:   requestbody.PositiveInt64FromEnvironment("ORCHESTRATOR_MAX_REQUEST_BODY_BYTES", defaultOrchestratorMaxRequestBodyBytes),
		MaxStudies:            int(requestbody.PositiveInt64FromEnvironment("ORCHESTRATOR_MAX_STUDIES_PER_REQUEST", defaultOrchestratorMaxStudies)),
		MaxSeriesPerStudy:     int(requestbody.PositiveInt64FromEnvironment("ORCHESTRATOR_MAX_SERIES_UIDS_PER_STUDY", defaultOrchestratorMaxSeriesPerStudy)),
		MaxPreviewBase64Bytes: int(requestbody.PositiveInt64FromEnvironment("ORCHESTRATOR_MAX_PREVIEW_BASE64_BYTES", defaultOrchestratorMaxPreviewBase64Bytes)),
		MaxMetadataBytes:      int(requestbody.PositiveInt64FromEnvironment("ORCHESTRATOR_MAX_METADATA_BYTES", defaultOrchestratorMaxMetadataBytes)),
		MaxMetadataDepth:      int(requestbody.PositiveInt64FromEnvironment("ORCHESTRATOR_MAX_METADATA_DEPTH", defaultOrchestratorMaxMetadataDepth)),
		MaxMetadataEntries:    int(requestbody.PositiveInt64FromEnvironment("ORCHESTRATOR_MAX_METADATA_ENTRIES", defaultOrchestratorMaxMetadataEntries)),
		MaxMessageBytes:       int(requestbody.PositiveInt64FromEnvironment("ORCHESTRATOR_MAX_MESSAGE_BYTES", defaultOrchestratorMaxMessageBytes)),
	}
}

func validateOrchestratorMessage(request httpTypes.CreateMessageRequest, limits orchestratorInputLimits) error {
	if len(request.Message) > limits.MaxMessageBytes || len(request.ImageData) > limits.MaxPreviewBase64Bytes || len(request.ImageType) > 64 {
		return errOrchestratorInputLimit
	}
	if err := inputlimits.ValidateJSONValue(request.Metadata, limits.MaxMetadataBytes, limits.MaxMetadataDepth, limits.MaxMetadataEntries); err != nil {
		return errOrchestratorInputLimit
	}
	return nil
}

func validateOrchestratorDICOMPayload(request httpTypes.UploadDicomPayloadRequest, limits orchestratorInputLimits) error {
	if len(request.Payload) == 0 {
		return errors.New(apiError.InvalidPayload)
	}
	if len(request.Payload) > limits.MaxStudies {
		return errOrchestratorInputLimit
	}
	seenStudies := make(map[string]struct{}, len(request.Payload))
	for _, study := range request.Payload {
		if !inputlimits.ValidDICOMUID(study.StudyInstanceUID) || len(study.SeriesInstanceUIDs) == 0 {
			return errors.New(apiError.InvalidPayload)
		}
		if _, duplicate := seenStudies[study.StudyInstanceUID]; duplicate {
			return errors.New(apiError.InvalidPayload)
		}
		seenStudies[study.StudyInstanceUID] = struct{}{}
		if len(study.SeriesInstanceUIDs) > limits.MaxSeriesPerStudy {
			return errOrchestratorInputLimit
		}
		seen := make(map[string]struct{}, len(study.SeriesInstanceUIDs))
		for _, uid := range study.SeriesInstanceUIDs {
			if !inputlimits.ValidDICOMUID(uid) {
				return errors.New(apiError.InvalidPayload)
			}
			if _, duplicate := seen[uid]; duplicate {
				return errors.New(apiError.InvalidPayload)
			}
			seen[uid] = struct{}{}
		}
		if err := inputlimits.ValidateJSONValue(study.AdditionalMetadata, limits.MaxMetadataBytes, limits.MaxMetadataDepth, limits.MaxMetadataEntries); err != nil {
			return errOrchestratorInputLimit
		}
		if study.PreviewImageBase64 != nil && len(*study.PreviewImageBase64) > limits.MaxPreviewBase64Bytes {
			return errOrchestratorInputLimit
		}
		if study.Modality != nil && len(*study.Modality) > 64 {
			return errOrchestratorInputLimit
		}
	}
	return nil
}

func writeOrchestratorDecodeError(writer http.ResponseWriter, request *http.Request, err error, endpoint string, maxBytes int64) {
	if requestbody.IsTooLarge(err) {
		requestbody.ObserveRejection(request, maxBytes, endpoint)
		requestbody.WriteTooLarge(writer)
		return
	}
	response := viewmodels.HTTPResponseVM{
		Status:    http.StatusBadRequest,
		Success:   false,
		Message:   "Invalid payload request.",
		ErrorCode: apiError.InvalidRequestPayload,
	}
	response.JSON(writer)
}

func writeOrchestratorInputError(writer http.ResponseWriter, err error, endpoint string) {
	statusCode := apiError.InvalidPayload
	message := "Invalid request payload."
	if errors.Is(err, errOrchestratorInputLimit) {
		statusCode = apiError.RequestInputLimitExceeded
		message = "Request input exceeds the configured safety limits."
	}
	log.Printf("[security] event=request_input_rejected endpoint=%s reason=%s", endpoint, statusCode)
	response := viewmodels.HTTPResponseVM{
		Status:    http.StatusBadRequest,
		Success:   false,
		Message:   message,
		ErrorCode: statusCode,
	}
	response.JSON(writer)
}
