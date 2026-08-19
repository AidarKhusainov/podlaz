package planner

import "strings"

const TunActionAddExclusive = "add-exclusive"

// IsTunAddAction reports whether a route/rule action represents a Podlaz add
// mutation. add-exclusive is used only for a newly allocated session so apply
// cannot adopt an object that appeared after the allocation snapshot.
func IsTunAddAction(action string) bool {
	switch strings.TrimSpace(action) {
	case "add", TunActionAddExclusive:
		return true
	default:
		return false
	}
}
