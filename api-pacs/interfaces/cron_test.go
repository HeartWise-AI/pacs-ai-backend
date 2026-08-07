package interfaces

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type reconciliationWorkerTestDouble struct {
	calls       int
	errors      []error
	cancelAfter int
	cancel      context.CancelFunc
}

func (worker *reconciliationWorkerTestDouble) ExecuteInferenceIngestionReconciliationWorker(context.Context) error {
	worker.calls++
	if worker.cancelAfter > 0 && worker.calls >= worker.cancelAfter {
		worker.cancel()
	}
	if worker.calls <= len(worker.errors) {
		return worker.errors[worker.calls-1]
	}
	return nil
}

func TestRunInferenceIngestionReconciliationWorkerExecutesImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &reconciliationWorkerTestDouble{cancelAfter: 1, cancel: cancel}

	runInferenceIngestionReconciliationWorker(ctx, worker, time.Hour)

	require.Equal(t, 1, worker.calls)
}

func TestRunInferenceIngestionReconciliationWorkerSchedulesAfterStartupFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	worker := &reconciliationWorkerTestDouble{
		errors:      []error{errors.New("study-service unavailable")},
		cancelAfter: 2,
		cancel:      cancel,
	}

	runInferenceIngestionReconciliationWorker(ctx, worker, time.Millisecond)

	require.Equal(t, 2, worker.calls)
}
