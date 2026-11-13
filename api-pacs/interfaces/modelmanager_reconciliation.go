package interfaces

import (
	"context"
	"log"
	"time"
)

// Retry only the failed startup inventory. Stop after the first successful
// reconciliation; rebuilding an active ledger would interrupt in-flight work.
func retryModelManagerReconciliation(ctx context.Context, reconcile func(context.Context) error, timeout, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			attemptCtx, cancel := context.WithTimeout(ctx, timeout)
			err := reconcile(attemptCtx)
			cancel()
			if err == nil {
				log.Print("[SERVER] model manager startup recovery completed; GPU admission available")
				return
			}
			log.Printf("[SERVER] GPU admission remains unavailable: %v", err)
		}
	}
}
