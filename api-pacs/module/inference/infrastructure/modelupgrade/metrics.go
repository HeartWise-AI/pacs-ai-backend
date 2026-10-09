package modelupgrade

import (
	"expvar"
	"time"

	"api-pacs/module/inference/domain/entity"
)

var (
	modelUpgradeAttemptsTotal       = expvar.NewMap("model_upgrade_attempts_total")
	modelUpgradeTransitionsTotal    = expvar.NewMap("model_upgrade_transitions_total")
	modelUpgradeFailuresTotal       = expvar.NewMap("model_upgrade_failures_total")
	modelUpgradeDurationCount       = expvar.NewMap("model_upgrade_duration_seconds_count")
	modelUpgradeDurationSumMillis   = expvar.NewMap("model_upgrade_duration_milliseconds_sum")
	modelUpgradeDrainDurationCount  = expvar.NewMap("model_upgrade_drain_duration_seconds_count")
	modelUpgradeDrainDurationMillis = expvar.NewMap("model_upgrade_drain_duration_milliseconds_sum")
)

type Metrics interface {
	ObserveTransition(entity.InferenceModelUpgradeState)
	ObserveFailure(entity.InferenceModelUpgradeState)
	ObserveDrain(string, time.Duration)
	ObserveCompletion(entity.InferenceModelUpgradeState, time.Duration)
}

type expvarMetrics struct{}

var defaultMetrics Metrics = expvarMetrics{}

func (expvarMetrics) ObserveTransition(state entity.InferenceModelUpgradeState) {
	modelUpgradeTransitionsTotal.Add("state="+string(state), 1)
}

func (expvarMetrics) ObserveFailure(state entity.InferenceModelUpgradeState) {
	modelUpgradeFailuresTotal.Add("stage="+string(state), 1)
}

func (expvarMetrics) ObserveDrain(outcome string, duration time.Duration) {
	modelUpgradeDrainDurationCount.Add("outcome="+outcome, 1)
	modelUpgradeDrainDurationMillis.Add("outcome="+outcome, duration.Milliseconds())
}

func (expvarMetrics) ObserveCompletion(state entity.InferenceModelUpgradeState, duration time.Duration) {
	key := "outcome=" + string(state)
	modelUpgradeAttemptsTotal.Add(key, 1)
	modelUpgradeDurationCount.Add(key, 1)
	modelUpgradeDurationSumMillis.Add(key, duration.Milliseconds())
}
