package api

// LogsPath is the local daemon endpoint used by the ordinary-user CLI to read
// redacted podlazd journal output without granting the caller broad system
// journal access.
const LogsPath = "/v1/logs"
