package doctor

import (
	"context"
	"fmt"
	"strings"
)

type routeDiagnostics struct {
	routeCheck     Check
	interfaceCheck Check
}

func defaultRoute(ctx context.Context, runner CommandRunner, ipPath string, ipOK bool) routeDiagnostics {
	if !ipOK {
		return routeDiagnostics{
			routeCheck: Check{
				Name:     "default-route",
				Severity: SeverityWarning,
				Message:  "cannot inspect default route because ip is unavailable",
			},
			interfaceCheck: Check{
				Name:     "default-interface",
				Severity: SeverityWarning,
				Message:  "cannot inspect default interface because ip is unavailable",
			},
		}
	}

	result, err := runCommand(ctx, runner, ipPath, "route", "show", "default")
	if !commandSucceeded(result, err) {
		message := fmt.Sprintf("ip route show default failed: %s", commandFailureMessage(result, err))
		return routeDiagnostics{
			routeCheck: Check{
				Name:     "default-route",
				Severity: SeverityFail,
				Message:  message,
			},
			interfaceCheck: Check{
				Name:     "default-interface",
				Severity: SeverityFail,
				Message:  "cannot inspect default interface because default route command failed",
			},
		}
	}

	routeLine := firstNonEmptyLine(result.Stdout)
	if routeLine == "" {
		return routeDiagnostics{
			routeCheck: Check{
				Name:     "default-route",
				Severity: SeverityWarning,
				Message:  "no default route found",
			},
			interfaceCheck: Check{
				Name:     "default-interface",
				Severity: SeverityWarning,
				Message:  "cannot inspect default interface because no default route was found",
			},
		}
	}

	iface, ok := parseRouteInterface(routeLine)
	if !ok {
		return routeDiagnostics{
			routeCheck: Check{
				Name:     "default-route",
				Severity: SeverityOK,
				Message:  routeLine,
			},
			interfaceCheck: Check{
				Name:     "default-interface",
				Severity: SeverityWarning,
				Message:  "default route has no dev field",
			},
		}
	}

	return routeDiagnostics{
		routeCheck: Check{
			Name:     "default-route",
			Severity: SeverityOK,
			Message:  routeLine,
		},
		interfaceCheck: Check{
			Name:     "default-interface",
			Severity: SeverityOK,
			Message:  iface,
		},
	}
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func parseRouteInterface(route string) (string, bool) {
	fields := strings.Fields(route)
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "dev" {
			return fields[i+1], true
		}
	}
	return "", false
}
