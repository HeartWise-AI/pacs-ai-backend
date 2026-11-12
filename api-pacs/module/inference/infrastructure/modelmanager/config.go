package modelmanager

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Config describes one host manager with one or more GPU budgets. BudgetMiB includes SafetyMarginMiB.
type Config struct {
	Enabled bool
	// RetainPeakWhileLoaded protects against allocator caches surviving inference.
	RetainPeakWhileLoaded bool
	GPUReservationPercent int
	RequireReconciliation bool
	BudgetMiB             int
	SafetyMarginMiB       int
	QueueTimeout          time.Duration
	OperationTimeout      time.Duration
	// GPUBudgetsMiB replaces the legacy single-device budget when set. Keys
	// are physical GPU UUIDs, never CUDA or nvidia-smi ordinals.
	GPUBudgetsMiB     map[string]int
	IdleSweepInterval time.Duration
}

func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Config{SafetyMarginMiB: 1024, QueueTimeout: 10 * time.Minute, OperationTimeout: 5 * time.Minute}
	if v := getenv("MODEL_MANAGER_ENABLED"); v != "" {
		var err error
		c.Enabled, err = strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("MODEL_MANAGER_ENABLED: %w", err)
		}
	}
	if !c.Enabled {
		return c, nil
	}
	c.RequireReconciliation = true
	c.RetainPeakWhileLoaded = true
	c.GPUReservationPercent = 100
	if v := getenv("MODEL_RETAIN_PEAK_WHILE_LOADED"); v != "" {
		var err error
		c.RetainPeakWhileLoaded, err = strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("MODEL_RETAIN_PEAK_WHILE_LOADED: %w", err)
		}
	}
	if v := getenv("GPU_MEMORY_BUDGETS_MIB"); v != "" {
		if err := json.Unmarshal([]byte(v), &c.GPUBudgetsMiB); err != nil || len(c.GPUBudgetsMiB) == 0 {
			return c, fmt.Errorf("GPU_MEMORY_BUDGETS_MIB must be a nonempty JSON object of GPU UUIDs to MiB budgets")
		}
	}
	if v := getenv("MODEL_IDLE_SWEEP_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return c, fmt.Errorf("MODEL_IDLE_SWEEP_SECONDS must be a non-negative integer")
		}
		c.IdleSweepInterval = time.Duration(n) * time.Second
	}
	for _, field := range []struct {
		name   string
		target *int
	}{
		{"GPU_MEMORY_BUDGET_MIB", &c.BudgetMiB},
		{"MODEL_GPU_MEMORY_RESERVATION_PERCENT", &c.GPUReservationPercent},
		{"GPU_MEMORY_SAFETY_MARGIN_MIB", &c.SafetyMarginMiB},
	} {
		if v := getenv(field.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return c, fmt.Errorf("%s: %w", field.name, err)
			}
			*field.target = n
		}
	}
	if v := getenv("MODEL_QUEUE_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n <= 0 {
			return c, fmt.Errorf("MODEL_QUEUE_TIMEOUT_SECONDS must be a positive integer")
		}
		c.QueueTimeout = time.Duration(n) * time.Second
	}
	if c.GPUReservationPercent < 100 {
		return c, fmt.Errorf("MODEL_GPU_MEMORY_RESERVATION_PERCENT must be between 100 and 1000")
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.GPUReservationPercent != 0 && (c.GPUReservationPercent < 100 || c.GPUReservationPercent > 1000) {
		return fmt.Errorf("MODEL_GPU_MEMORY_RESERVATION_PERCENT must be between 100 and 1000")
	}
	if c.IdleSweepInterval < 0 {
		return fmt.Errorf("idle sweep interval must be non-negative")
	}
	if len(c.GPUBudgetsMiB) > 0 {
		for uuid, budget := range c.GPUBudgetsMiB {
			if !gpuUUIDPattern.MatchString(uuid) || c.SafetyMarginMiB < 0 || budget <= c.SafetyMarginMiB {
				return fmt.Errorf("each GPU UUID budget must exceed its non-negative safety margin")
			}
		}
	} else if c.BudgetMiB <= 0 || c.SafetyMarginMiB < 0 || c.SafetyMarginMiB >= c.BudgetMiB {
		return fmt.Errorf("GPU budget must exceed its non-negative safety margin")
	}
	if c.QueueTimeout <= 0 || c.OperationTimeout <= 0 {
		return fmt.Errorf("manager timeouts must be positive")
	}
	return nil
}

var gpuUUIDPattern = regexp.MustCompile(`^GPU-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
