package status

// LifecycleHealth is the typed diagnostic health of the published connection
// lifecycle. Runtime warnings do not determine this value.
type LifecycleHealth uint8

const (
	LifecycleHealthUnknown LifecycleHealth = iota
	LifecycleHealthHealthy
	LifecycleHealthUnhealthy
)

// Health returns the diagnostic lifecycle health represented by Connection.
// The stable terminal/steady states active and inactive are healthy. Any other
// non-empty lifecycle state is not a healthy steady state and makes status
// diagnostic output unhealthy. The zero value remains unknown so zero-value
// reports used internally do not become failures by accident.
func (r Report) Health() LifecycleHealth {
	switch r.Connection {
	case "active", "inactive":
		return LifecycleHealthHealthy
	case "":
		return LifecycleHealthUnknown
	default:
		return LifecycleHealthUnhealthy
	}
}
