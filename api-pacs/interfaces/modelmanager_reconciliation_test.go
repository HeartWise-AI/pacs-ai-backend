package interfaces

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartupReconciliationRetriesWithBoundedContextAndStopsOnRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	retryModelManagerReconciliation(ctx, func(attempt context.Context) error {
		calls++
		deadline, ok := attempt.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 50*time.Millisecond)
		if calls == 1 {
			return errors.New("legacy model lacks resource contract")
		}
		return nil
	}, 50*time.Millisecond, time.Millisecond)
	require.Equal(t, 2, calls)
	require.NoError(t, ctx.Err(), "recovery must stop without waiting for shutdown")
}

func TestStartupReconciliationDoesNotRunAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	retryModelManagerReconciliation(ctx, func(context.Context) error {
		t.Fatal("canceled retry must not touch Firestore or Docker")
		return nil
	}, time.Second, time.Hour)
}
