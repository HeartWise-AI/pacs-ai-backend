package hystrix

import (
	"github.com/afex/hystrix-go/hystrix"
)

// Config handles the hystrix configurations
type Config struct{}

// Settings returns the hystrix command config
func (c Config) Settings() hystrix.CommandConfig {
	return hystrix.CommandConfig{
		MaxConcurrentRequests:  200,
		Timeout:                8000,
		RequestVolumeThreshold: 1000,
	}
}
