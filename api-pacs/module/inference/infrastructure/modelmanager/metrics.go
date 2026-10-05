package modelmanager

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"strings"
	"time"
)

var (
	modelManagerReady                = expvar.NewInt("model_manager_ready")
	modelManagerModels               = expvar.NewMap("model_manager_models")
	modelManagerLoadedModels         = expvar.NewInt("model_manager_loaded_models")
	modelManagerGPUMemoryMiB         = expvar.NewMap("model_manager_gpu_memory_mib")
	modelManagerQueueDepth           = expvar.NewInt("model_manager_queue_depth")
	modelManagerReconciliationsTotal = expvar.NewMap("model_manager_reconciliations_total")
	modelManagerEventsTotal          = expvar.NewMap("model_manager_events_total")
	modelManagerDurationCount        = expvar.NewMap("model_manager_duration_seconds_count")
	modelManagerDurationSumMillis    = expvar.NewMap("model_manager_duration_milliseconds_sum")
	modelManagerDurationBucket       = expvar.NewMap("model_manager_duration_seconds_bucket")
)

var modelManagerDurationBuckets = []time.Duration{
	10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond,
	500 * time.Millisecond, time.Second, 5 * time.Second, 30 * time.Second,
	60 * time.Second, 300 * time.Second,
}

type Metrics interface {
	SetSnapshot(Snapshot)
	ObserveReconciliation(string, time.Duration)
	ObserveQueueWait(string, time.Duration)
	ObserveOperation(string, string, time.Duration)
	ObserveEvent(string, string)
}

type expvarMetrics struct{}

var defaultMetrics Metrics = expvarMetrics{}

func (expvarMetrics) SetSnapshot(snapshot Snapshot) {
	if snapshot.Ready {
		modelManagerReady.Set(1)
	} else {
		modelManagerReady.Set(0)
	}
	states := map[string]int64{
		"unloaded": 0, "loading": 0, "ready": 0,
		"busy": 0, "evicting": 0, "error": 0,
	}
	loaded := int64(0)
	for _, model := range snapshot.Models {
		states[strings.ToLower(string(model.State))]++
		if model.Loaded {
			loaded++
		}
	}
	for state, count := range states {
		setMapInt(modelManagerModels, "state="+state, count)
	}
	modelManagerLoadedModels.Set(loaded)
	setMapInt(modelManagerGPUMemoryMiB, "kind=capacity", int64(snapshot.CapacityMiB))
	setMapInt(modelManagerGPUMemoryMiB, "kind=resident", int64(snapshot.ResidentMiB))
	setMapInt(modelManagerGPUMemoryMiB, "kind=active", int64(snapshot.ActiveMiB))
	setMapInt(modelManagerGPUMemoryMiB, "kind=reserved", int64(snapshot.ReservedMiB))
	setMapInt(modelManagerGPUMemoryMiB, "kind=available", int64(snapshot.AvailableMiB))
	modelManagerQueueDepth.Set(int64(snapshot.QueueDepth))
}

func (expvarMetrics) ObserveReconciliation(outcome string, duration time.Duration) {
	modelManagerReconciliationsTotal.Add(outcome, 1)
	observeModelManagerDuration("operation=reconciliation,outcome="+outcome, duration)
}

func (expvarMetrics) ObserveQueueWait(outcome string, duration time.Duration) {
	observeModelManagerDuration("operation=queue_wait,outcome="+outcome, duration)
}

func (expvarMetrics) ObserveOperation(operation, outcome string, duration time.Duration) {
	observeModelManagerDuration(fmt.Sprintf("operation=%s,outcome=%s", operation, outcome), duration)
}

func (expvarMetrics) ObserveEvent(event, outcome string) {
	modelManagerEventsTotal.Add(fmt.Sprintf("event=%s,outcome=%s", event, outcome), 1)
}

func observeModelManagerDuration(key string, duration time.Duration) {
	modelManagerDurationCount.Add(key, 1)
	modelManagerDurationSumMillis.Add(key, duration.Milliseconds())
	for _, bucket := range modelManagerDurationBuckets {
		if duration <= bucket {
			modelManagerDurationBucket.Add(fmt.Sprintf("%s,le=%g", key, bucket.Seconds()), 1)
		}
	}
	modelManagerDurationBucket.Add(key+",le=+Inf", 1)
}

func setMapInt(metric *expvar.Map, key string, value int64) {
	counter := new(expvar.Int)
	counter.Set(value)
	metric.Set(key, counter)
}

func metricOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, ErrQueueTimeout), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, ErrCapacity):
		return "capacity"
	case errors.Is(err, ErrNotReconciled):
		return "not_reconciled"
	default:
		return "failed"
	}
}
