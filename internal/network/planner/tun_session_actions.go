package planner

import "strings"

const TunActionAddExclusive = "add-exclusive"
const TunActionVerifyExisting = "verify-existing"
const TunAddressActionAssignExclusive = "assign-exclusive"

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

// IsTunAddressAssignAction reports whether an address action represents a
// Podlaz assignment. assign-exclusive is used only for a newly allocated
// Network Session and requires a live global collision fence around mutation.
func IsTunAddressAssignAction(action string) bool {
	switch strings.TrimSpace(action) {
	case TunAddressActionAssign, TunAddressActionAssignExclusive:
		return true
	default:
		return false
	}
}

func IsTunAddressExclusiveAction(action string) bool {
	return strings.TrimSpace(action) == TunAddressActionAssignExclusive
}

// IsTunVerifyAction reports whether a planned object is an exact host
// prerequisite that must remain present but is not created or owned by Podlaz.
func IsTunVerifyAction(action string) bool {
	return strings.TrimSpace(action) == TunActionVerifyExisting
}

func IsTunVerifyOrAddAction(action string) bool {
	return IsTunAddAction(action) || IsTunVerifyAction(action)
}
