package orthanc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
)

// The move supervisor drives the tracked moves when nobody reads them:
// it sends waiting background moves when a background slot is free and reads
// every unfinished move, which retries and checks it (GetJobInfo).

// Background moves allowed in Orthanc at once (pending or running). Orthanc
// runs ORTHANC__CONCURRENT_JOBS (4 on MHI) jobs; one less keeps a slot for a
// study someone is waiting for. ORTHANC_BACKGROUND_MOVE_SLOTS overrides it.
const defaultBackgroundMoveSlots = 3

const moveSupervisorInterval = 5 * time.Second

func backgroundMoveSlots() int {
	if value, err := strconv.Atoi(os.Getenv("ORTHANC_BACKGROUND_MOVE_SLOTS")); err == nil && value > 0 {
		return value
	}
	return defaultBackgroundMoveSlots
}

// One dispatcher at a time, so two callers cannot both fill the last slot.
var dispatchMu sync.Mutex

// UseMoveStore saves the tracked moves in Postgres and loads those saved
// before a restart. Call it once at start-up, before SuperviseMoves.
func UseMoveStore(store moveStore) error {
	return seriesMoves.useStore(store)
}

// SuperviseMoves runs until ctx ends.
func (o *OrthancAPI) SuperviseMoves(ctx context.Context) {
	ticker := time.NewTicker(moveSupervisorInterval)
	defer ticker.Stop()
	lastPurge := time.Time{}
	for {
		o.superviseOnce(ctx)
		if time.Since(lastPurge) > 10*time.Minute {
			seriesMoves.purge()
			lastPurge = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (o *OrthancAPI) superviseOnce(ctx context.Context) {
	if err := o.DispatchBackgroundMoves(); err != nil {
		log.Printf("[retrieve] cannot send waiting background moves: %v", err)
	}
	for _, jobID := range seriesMoves.active() {
		if ctx.Err() != nil {
			return
		}
		if _, err := o.GetJobInfo(ctx, jobID); err != nil {
			log.Printf("[retrieve] cannot read move job %s: %v", jobID, err)
		}
	}
}

// purge forgets moves older than trackedMoveLifetime, in memory and in the store.
func (t *moveTracker) purge() {
	t.mu.Lock()
	now := t.now()
	t.forgetOldLocked(now)
	store := t.store
	t.mu.Unlock()
	if store != nil {
		if err := store.DeleteMovesBefore(now.Add(-trackedMoveLifetime)); err != nil {
			log.Printf("[retrieve] cannot delete old saved moves: %v", err)
		}
	}
}

// DispatchBackgroundMoves sends waiting background moves to Orthanc while
// fewer than backgroundMoveSlots background moves are pending or running there.
func (o *OrthancAPI) DispatchBackgroundMoves() error {
	dispatchMu.Lock()
	defer dispatchMu.Unlock()

	waiting := seriesMoves.waiting()
	if len(waiting) == 0 {
		return nil
	}
	busy, err := o.backgroundMovesInOrthanc()
	if err != nil {
		return err
	}
	free := backgroundMoveSlots() - busy
	for _, move := range waiting {
		if free <= 0 {
			return nil
		}
		if o.allStored(move.parts) {
			// Another retrieve (the viewer) brought the images meanwhile.
			seriesMoves.completeWithoutMove(move.jobID)
			continue
		}
		answer, err := o.submitSeriesMove(move.modalityID, move.request)
		if err != nil {
			log.Printf("[retrieve] cannot send waiting move job %s: %v", move.jobID, err)
			seriesMoves.dispatchFailed(move.jobID, err.Error())
			continue
		}
		seriesMoves.dispatched(move.jobID, answer.ID)
		free--
	}
	return nil
}

// backgroundMovesInOrthanc counts the background (below viewer priority)
// move jobs pending or running in Orthanc, whoever started them.
func (o *OrthancAPI) backgroundMovesInOrthanc() (int, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/jobs?expand", o.BaseURL), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, errors.New(apiError.OrthancError)
	}
	var jobs []types.GetJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return 0, err
	}
	return countBackgroundMoves(jobs), nil
}

func countBackgroundMoves(jobs []types.GetJobResponse) int {
	busy := 0
	for _, job := range jobs {
		if job.Type != dicomMoveJobType || job.Priority >= interactiveMovePriority {
			continue
		}
		switch job.State {
		case string(types.JobPending), string(types.JobRunning), "Retry", "Paused":
			busy++
		}
	}
	return busy
}

// allStored is true when every series of a move has all the images the PACS
// announced. A series without a count is never taken as complete.
func (o *OrthancAPI) allStored(parts []movePart) bool {
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if part.expected == 0 {
			return false
		}
		count, err := o.countLocalSeriesInstances(part.studyInstanceUID, part.seriesInstanceUID)
		if err != nil || count < part.expected {
			return false
		}
	}
	return true
}
