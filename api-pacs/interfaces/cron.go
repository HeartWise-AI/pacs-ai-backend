package interfaces

import (
	"context"
	"log"
	"time"

	apiError "api-pacs/internal/errors"
)

// RunOrthancLocalResourceCacheHandler run orthanc local resource cache handler
func RunOrthancLocalResourceCacheHandler() {
	orthancCommandService := OrthancCommandServiceDI()

	// run every 1hr
	tick := time.Tick(1 * time.Hour)
	for range tick {
		err := orthancCommandService.ClearLocalResourcesCache(context.TODO())
		if err == nil || err.Error() == apiError.MissingRecord {
			log.Println("[Cache] executed local resource cache purge")
		} else {
			log.Println("[Cache] error while executing local resource cache purge:", err)
		}
	}
}
