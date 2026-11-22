package modelmanager

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"math"
	"strings"
	"sync"
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
	modelManagerGPUProcessMemoryMiB  = expvar.NewMap("model_manager_gpu_process_memory_mib")
	modelManagerReservationMiB       = expvar.NewMap("model_manager_reservation_mib")
	modelManagerMeasuredModels       = expvar.NewMap("model_manager_measured_models")
	modelManagerOvershootsTotal      = expvar.NewInt("model_manager_reservation_overshoots_total")
)

// gpuLastSampled holds, per GPU, when its processes were last read. The age
// gauge is computed when /debug/vars is read, so it keeps growing if the
// sampler stops; -1 means the GPU has never been sampled.
var gpuLastSampled sync.Map

func gpuSampleAges() map[string]float64 {
	ages := map[string]float64{}
	gpuLastSampled.Range(func(key, value any) bool {
		age := -1.0
		if at := value.(time.Time); !at.IsZero() {
			age = math.Round(time.Since(at).Seconds()*10) / 10
		}
		ages["gpu="+key.(string)] = age
		return true
	})
	return ages
}

func init() {
	expvar.Publish("model_manager_gpu_sample_age_seconds", expvar.Func(func() any { return gpuSampleAges() }))
}

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

// measurementMetrics is implemented by metric sinks that also publish the
// measured-memory gauges. It is optional so existing sinks keep working.
type measurementMetrics interface {
	SetMeasurements(MeasurementReport)
}

type expvarMetrics struct{}

// SetMeasurements publishes sampled process memory per device and the total
// reservation under each source. Like the other gauges it carries no model or
// container identifier; per-model values are served by the GPU status endpoint.
func (expvarMetrics) SetMeasurements(report MeasurementReport) {
	for uuid, usage := range report.GPUs {
		gpuLastSampled.Store(uuid, usage.SampledAt)
		if usage.SampledAt.IsZero() {
			continue
		}
		setMapInt(modelManagerGPUProcessMemoryMiB, "gpu="+uuid+",kind=managed", int64(usage.ManagedMiB))
		setMapInt(modelManagerGPUProcessMemoryMiB, "gpu="+uuid+",kind=foreign", int64(usage.ForeignMiB))
		setMapInt(modelManagerGPUProcessMemoryMiB, "gpu="+uuid+",kind=unattributed", int64(usage.UnattributedMiB))
	}
	var declared, effective, measured, observedPeak, sampled, eligible, overshoots int64
	for _, model := range report.Models {
		overshoots += int64(model.Overshoots)
		if !model.ResourcesKnown || !model.GPURequired {
			continue
		}
		declared += int64(model.DeclaredReservationMiB)
		effective += int64(model.ReservationMiB)
		observedPeak += int64(model.MeasuredPeakMiB)
		if model.Sampled {
			sampled++
		}
		// "measured" is what MODEL_RESERVATION_SOURCE=measured would reserve
		// today: models without enough samples still count at their declaration.
		if model.MeasuredReservationMiB > 0 {
			eligible++
			measured += int64(model.MeasuredReservationMiB)
		} else {
			measured += int64(model.DeclaredReservationMiB)
		}
	}
	setMapInt(modelManagerReservationMiB, "kind=declared", declared)
	setMapInt(modelManagerReservationMiB, "kind=effective", effective)
	setMapInt(modelManagerReservationMiB, "kind=measured", measured)
	setMapInt(modelManagerReservationMiB, "kind=observed_peak", observedPeak)
	setMapInt(modelManagerMeasuredModels, "kind=sampled", sampled)
	setMapInt(modelManagerMeasuredModels, "kind=eligible", eligible)
	modelManagerOvershootsTotal.Set(overshoots)
}

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
	for uuid, gpu := range snapshot.GPUs {
		for kind, value := range map[string]int{"capacity": gpu.CapacityMiB, "resident": gpu.ResidentMiB, "active": gpu.ActiveMiB, "reserved": gpu.ReservedMiB, "available": gpu.AvailableMiB} {
			setMapInt(modelManagerGPUMemoryMiB, "gpu="+uuid+",kind="+kind, int64(value))
		}
	}
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
	case errors.Is(err, ErrAdmissionBlocked):
		return "blocked"
	case errors.Is(err, ErrNotReconciled):
		return "not_reconciled"
	default:
		return "failed"
	}
}
