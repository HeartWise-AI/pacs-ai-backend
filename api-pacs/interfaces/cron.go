package interfaces

import (
	"context"
	"log"
	"time"

	apiError "api-pacs/internal/errors"
)

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
