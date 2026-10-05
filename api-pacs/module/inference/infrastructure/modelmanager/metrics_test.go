package modelmanager

import (
	"expvar"
	"testing"
	"time"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
)

func TestMetricsExposeOnlyAggregateStateAndMemory(t *testing.T) {
	metrics := expvarMetrics{}
	metrics.SetSnapshot(Snapshot{
		Ready: true, CapacityMiB: 100, ResidentMiB: 30, ActiveMiB: 50,
		ReservedMiB: 80, AvailableMiB: 20, QueueDepth: 2,
		Models: map[string]ModelSnapshot{
			id(1): {State: types.RuntimeBusy, Loaded: true},
			id(2): {State: types.RuntimeUnloaded},
		},
	})
	if modelManagerReady.Value() != 1 || modelManagerLoadedModels.Value() != 1 || modelManagerQueueDepth.Value() != 2 {
		t.Fatal("aggregate gauges were not updated")
	}
	if expvarMapValue(modelManagerModels, "state=busy") != 1 || expvarMapValue(modelManagerGPUMemoryMiB, "kind=available") != 20 {
		t.Fatal("state or memory gauges were not updated")
	}
	if modelManagerModels.Get(id(1)) != nil {
		t.Fatal("container identifier leaked into metrics")
	}

	before := expvarMapValue(modelManagerDurationCount, "operation=warm_inference,outcome=success")
	metrics.ObserveOperation("warm_inference", "success", 25*time.Millisecond)
	if expvarMapValue(modelManagerDurationCount, "operation=warm_inference,outcome=success") != before+1 {
		t.Fatal("duration metric was not recorded")
	}
}

func expvarMapValue(metric *expvar.Map, key string) int64 {
	value := metric.Get(key)
	if value == nil {
		return 0
	}
	counter, ok := value.(*expvar.Int)
	if !ok {
		return 0
	}
	return counter.Value()
}
