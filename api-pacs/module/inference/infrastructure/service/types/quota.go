package types

import "time"

// InferenceQuotaReservation represents one user-triggered unit of inference
// work. ReservationID makes release and refund operations idempotent.
type InferenceQuotaReservation struct {
	TenantID      string
	UserID        string
	ReservationID string
	Units         int64
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
}
