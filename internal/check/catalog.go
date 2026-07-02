package check

import "time"

const (
	SchemaVersion = "v1"
	DefaultTimeout = 3 * time.Second
	DefaultBatchConcurrency = 1
	ProbeTypeHTTPS = "https"
)
