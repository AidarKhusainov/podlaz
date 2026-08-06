package recovery

import "strings"

func rollbackRouteRequiresTunIdentity(table, dev string) bool {
	if strings.TrimSpace(dev) == managedInterface {
		return true
	}
	_, managed := managedTableToken(table)
	return managed
}
