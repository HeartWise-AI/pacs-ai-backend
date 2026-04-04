package interfaces

import (
	"context"
	"log"
	"time"

	apiError "api-pacs/internal/errors"
)

// RunInferenceIngestionServiceHandler run inference ingestion service handler
func RunInferenceIngestionServiceHandler() {
	inferenceCommandService := InferenceCommandServiceDI()

	// run every 5 mins
	tick := time.Tick(5 * time.Minute)
	for range tick {
		err := inferenceCommandService.ExecuteInferenceIngestionRunner(context.TODO())
		if err == nil || err.Error() == apiError.MissingRecord {
			log.Println("[Ingestion] executed inference ingestion runner")
		} else {
			log.Println("[Ingestion] error while executing inference ingestion runner:", err)
		}
	}
}

// RunOrthancLocalStudiesCacheHandler run orthanc local studies cache handler
func RunOrthancLocalStudiesCacheHandler() {
	orthancCommandService := OrthancCommandServiceDI()

	// run every 1hr
	tick := time.Tick(1 * time.Hour)
	for range tick {
		err := orthancCommandService.ClearLocalStudiesCache(context.TODO())
		if err == nil || err.Error() == apiError.MissingRecord {
			log.Println("[Cache] executed local studies cache purge")
		} else {
			log.Println("[Cache] error while executing local studies cache purge:", err)
		}
	}
}
