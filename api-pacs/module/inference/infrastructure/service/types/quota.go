package types

import "time"

// InferenceQuotaReservationKind selects which active-execution cap a
// reservation is admitted against. The zero value is interactive.
type InferenceQuotaReservationKind string

const (
	// InferenceQuotaReservationInteractive is a synchronous predict that holds
	// an HTTP request open while a model container runs. It counts against the
	// allowance and the small per-user concurrent-execution cap.
	InferenceQuotaReservationInteractive InferenceQuotaReservationKind = "interactive"
	// InferenceQuotaReservationQueued is managed work (viewer runs, manual
	// reprocess) handed to study-service, whose per-model queues and model
	// maxConcurrentInferences bound real capacity. It counts against the
	// allowance and a much higher per-user queued-run safety cap only.
	InferenceQuotaReservationQueued InferenceQuotaReservationKind = "queued"
)

// InferenceQuotaReservation represents one user-triggered unit of inference
// work. ReservationID makes release and refund operations idempotent. Kind is
// only consulted by Reserve; Release and Refund find the reservation by ID
// whichever kind it was admitted as.
type InferenceQuotaReservation struct {
	TenantID      string
	UserID        string
	ReservationID string
	Units         int64
	Kind          InferenceQuotaReservationKind
}

// IsQueued reports whether the reservation is admitted as queued work.
func (reservation InferenceQuotaReservation) IsQueued() bool {
	return reservation.Kind == InferenceQuotaReservationQueued
}

// InferenceQuotaStatus is the machine-readable state returned after every
// quota operation and by the authenticated quota endpoint.
type InferenceQuotaStatus struct {
	Allowance               int64
	Used                    int64
	Remaining               int64
	Window                  time.Duration
	ResetAfter              time.Duration
	MaxConcurrentExecutions int64
	ActiveExecutions        int64
	ConcurrentRetryAfter    time.Duration
	MaxQueuedExecutions     int64
	QueuedExecutions        int64
	QueuedRetryAfter        time.Duration
}
