package modelupgrade

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/distribution/reference"
)

type Config struct {
	AllowedNamespace string
	MaxDrainTimeout  time.Duration
	OperationTimeout time.Duration
	ReadinessTimeout time.Duration
	PollInterval     time.Duration
}

func ConfigFromEnv() (Config, error) {
	config := Config{
		AllowedNamespace: "heartwisehub",
		MaxDrainTimeout:  15 * time.Minute,
		OperationTimeout: 30 * time.Minute,
		ReadinessTimeout: 2 * time.Minute,
		PollInterval:     500 * time.Millisecond,
	}
	if value := strings.TrimSpace(os.Getenv("MODEL_UPGRADE_ALLOWED_NAMESPACE")); value != "" {
		config.AllowedNamespace = value
	}
	for _, item := range []struct {
		name   string
		target *time.Duration
	}{
		{"MODEL_UPGRADE_MAX_DRAIN_SECONDS", &config.MaxDrainTimeout},
		{"MODEL_UPGRADE_OPERATION_TIMEOUT_SECONDS", &config.OperationTimeout},
		{"MODEL_UPGRADE_READINESS_TIMEOUT_SECONDS", &config.ReadinessTimeout},
	} {
		if value := strings.TrimSpace(os.Getenv(item.name)); value != "" {
			seconds, err := strconv.Atoi(value)
			if err != nil || seconds <= 0 {
				return config, fmt.Errorf("%s must be a positive integer", item.name)
			}
			*item.target = time.Duration(seconds) * time.Second
		}
	}
	return config, config.Validate()
}

func (config Config) Validate() error {
	if strings.TrimSpace(config.AllowedNamespace) == "" {
		return fmt.Errorf("model upgrade registry namespace is required")
	}
	if _, err := reference.ParseNormalizedNamed(strings.TrimSuffix(strings.TrimSpace(config.AllowedNamespace), "/") + "/allowed-image"); err != nil {
		return fmt.Errorf("model upgrade registry namespace is invalid")
	}
	if config.MaxDrainTimeout <= 0 || config.OperationTimeout <= 0 || config.ReadinessTimeout <= 0 || config.PollInterval <= 0 {
		return fmt.Errorf("model upgrade timeouts must be positive")
	}
	if config.MaxDrainTimeout > config.OperationTimeout || config.ReadinessTimeout > config.OperationTimeout {
		return fmt.Errorf("model upgrade sub-operation timeouts cannot exceed the operation timeout")
	}
	return nil
}
