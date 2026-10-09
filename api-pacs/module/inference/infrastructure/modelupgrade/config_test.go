package modelupgrade

import (
	"testing"
	"time"
)

func TestConfigFromEnvUsesSafeDefaultsAndOverrides(t *testing.T) {
	t.Setenv("MODEL_UPGRADE_ALLOWED_NAMESPACE", "registry.example/heartwise")
	t.Setenv("MODEL_UPGRADE_MAX_DRAIN_SECONDS", "60")
	t.Setenv("MODEL_UPGRADE_OPERATION_TIMEOUT_SECONDS", "120")
	t.Setenv("MODEL_UPGRADE_READINESS_TIMEOUT_SECONDS", "30")
	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.AllowedNamespace != "registry.example/heartwise" || config.MaxDrainTimeout != time.Minute || config.OperationTimeout != 2*time.Minute || config.ReadinessTimeout != 30*time.Second {
		t.Fatalf("unexpected configuration: %+v", config)
	}
}

func TestConfigRejectsSubOperationTimeoutBeyondOverallBound(t *testing.T) {
	config := Config{
		AllowedNamespace: "heartwisehub", MaxDrainTimeout: 2 * time.Minute,
		OperationTimeout: time.Minute, ReadinessTimeout: 30 * time.Second, PollInterval: time.Millisecond,
	}
	if err := config.Validate(); err == nil {
		t.Fatal("incoherent timeout configuration was accepted")
	}
}
