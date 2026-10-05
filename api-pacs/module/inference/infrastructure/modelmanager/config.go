package modelmanager

import (
	"fmt"
	"strconv"
	"time"
)

// Config describes one manager and one GPU. BudgetMiB includes SafetyMarginMiB.
type Config struct {
	Enabled               bool
	RequireReconciliation bool
	BudgetMiB             int
	SafetyMarginMiB       int
	QueueTimeout          time.Duration
	OperationTimeout      time.Duration
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
	for _, field := range []struct {
		name   string
		target *int
	}{
		{"GPU_MEMORY_BUDGET_MIB", &c.BudgetMiB},
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
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.BudgetMiB <= 0 || c.SafetyMarginMiB < 0 || c.SafetyMarginMiB >= c.BudgetMiB {
		return fmt.Errorf("GPU budget must exceed its non-negative safety margin")
	}
	if c.QueueTimeout <= 0 || c.OperationTimeout <= 0 {
		return fmt.Errorf("manager timeouts must be positive")
	}
	return nil
}
