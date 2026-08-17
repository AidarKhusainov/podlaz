package api

const (
	// LogsPath is the local daemon endpoint used by the ordinary-user CLI to
	// read redacted podlazd journal output without granting broad journal access.
	LogsPath = "/v1/logs"

	// LogsErrorTrailer reports a generic backend-stream failure after HTTP 200
	// has already been committed. Detailed journal errors never cross the daemon
	// API boundary.
	LogsErrorTrailer = "X-Podlaz-Logs-Error"
)
