package types

import "testing"

func TestLifecycleFromStatus(t *testing.T) {
	tests := map[Status]Lifecycle{
		StatusCreated:    LifecycleStarting,
		StatusRestarting: LifecycleStarting,
		StatusRunning:    LifecycleRunning,
		StatusPaused:     LifecyclePaused,
		StatusRemoving:   LifecycleStopping,
		StatusExited:     LifecycleStopped,
		StatusDead:       LifecycleDegraded,
		"unexpected":     LifecycleUnknown,
	}

	for status, expected := range tests {
		if actual := LifecycleFromStatus(status); actual != expected {
			t.Errorf("LifecycleFromStatus(%q) = %q, want %q", status, actual, expected)
		}
	}
}
