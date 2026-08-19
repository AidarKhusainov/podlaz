package daemon

// ShutdownIntent distinguishes a product restart/upgrade continuation from an
// explicit stop. It is process-lifecycle input only; it never grants network
// cleanup authority.
type ShutdownIntent string

const (
	ShutdownStop    ShutdownIntent = "stop"
	ShutdownRestart ShutdownIntent = "restart"
)

func normalizeShutdownIntent(intent ShutdownIntent) ShutdownIntent {
	if intent == ShutdownRestart {
		return ShutdownRestart
	}
	return ShutdownStop
}
