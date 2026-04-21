package interfaces

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	apiError "api-pacs/internal/errors"
)

// RunInferenceIngestionServiceHandler run inference ingestion service handler
func RunInferenceIngestionServiceHandler() {
	inferenceCommandService := InferenceCommandServiceDI()
	interval := inferenceIngestionRunnerInterval()

	log.Printf("[Ingestion] runner interval configured minutes=%d", int(interval.Minutes()))

	tick := time.Tick(interval)
	for range tick {
		err := inferenceCommandService.ExecuteInferenceIngestionRunner(context.TODO())
		if err == nil || err.Error() == apiError.MissingRecord {
			log.Println("[Ingestion] executed inference ingestion runner")
		} else {
			log.Println("[Ingestion] error while executing inference ingestion runner:", err)
		}
	}
}

func inferenceIngestionRunnerInterval() time.Duration {
	const defaultIntervalMinutes = 1

	value := strings.TrimSpace(os.Getenv("INFERENCE_INGESTION_RUNNER_INTERVAL_MINUTES"))
	if value == "" {
		log.Printf("[Ingestion] INFERENCE_INGESTION_RUNNER_INTERVAL_MINUTES not set, using default minutes=%d", defaultIntervalMinutes)
		return defaultIntervalMinutes * time.Minute
	}

	minutes, err := strconv.Atoi(value)
	if err != nil || minutes <= 0 {
		log.Printf("[Ingestion] invalid INFERENCE_INGESTION_RUNNER_INTERVAL_MINUTES=%q, using default minutes=%d", value, defaultIntervalMinutes)
		return defaultIntervalMinutes * time.Minute
	}

	log.Printf("[Ingestion] using INFERENCE_INGESTION_RUNNER_INTERVAL_MINUTES=%d", minutes)
	return time.Duration(minutes) * time.Minute
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
