package config

import "time"

type TagBatchConfig struct {
	BatchSize            int
	MaxConcurrentBatches int
	MaxRetries           int
	RetryBaseDelay       time.Duration
	BatchTimeout         time.Duration
}
