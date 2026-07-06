package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type runtimeHelperDiagnostic struct {
	name       string
	envName    string
	command    string
	versionArg []string
}

func runtimeHelperChecks(ctx context.Context, runner CommandRunner) []Check {
	configured := []runtimeHelperDiagnostic{
		{name: "xray runtime", envName: "PODLAZ_XRAY_PATH", command: "xray", versionArg: []string{"version"}},
	}
	checks := make([]Check, 0, len(configured))
	for _, helper := range configured {
		if strings.TrimSpace(os.Getenv(helper.envName)) == "" {
			continue
		}
		checks = append(checks, runtimeHelperCheck(ctx, runner, helper))
	}
	return checks
}

func runtimeHelperCheck(ctx context.Context, runner CommandRunner, d runtimeHelperDiagnostic) Check {
	configured := strings.TrimSpace(os.Getenv(d.envName))
	path := configured
	if path == "" {
		resolved, err := runner.LookPath(d.command)
		if err != nil {
			return Check{Name: d.name, Severity: SeverityFail, Message: fmt.Sprintf("%s not found; set %s or install the full podlaz package", d.command, d.envName)}
		}
		path = resolved
	}

	if strings.ContainsRune(path, os.PathSeparator) {
		info, err := os.Stat(path)
		if err != nil {
			return Check{Name: d.name, Severity: SeverityFail, Message: fmt.Sprintf("%s is not available at %s: %v", d.command, path, err)}
		}
		if info.IsDir() {
			return Check{Name: d.name, Severity: SeverityFail, Message: fmt.Sprintf("%s path is a directory: %s", d.command, path)}
		}
		if info.Mode().Perm()&0o111 == 0 {
			return Check{Name: d.name, Severity: SeverityFail, Message: fmt.Sprintf("%s is not executable: %s", d.command, path)}
		}
	} else {
		resolved, err := runner.LookPath(path)
		if err != nil {
			return Check{Name: d.name, Severity: SeverityFail, Message: fmt.Sprintf("%s not found in PATH: %v", path, err)}
		}
		path = resolved
	}

	message := fmt.Sprintf("found at %s", path)
	if version := runtimeHelperVersion(ctx, runner, path, d.versionArg); version != "" {
		message += "; " + version
	}
	return Check{Name: d.name, Severity: SeverityOK, Message: message}
}

func runtimeHelperVersion(ctx context.Context, runner CommandRunner, path string, args []string) string {
	if len(args) == 0 {
		return ""
	}
	result, err := runCommand(ctx, runner, path, args...)
	if commandFailedUnexpectedly(result, err) {
		return "version unavailable"
	}
	text := strings.TrimSpace(result.Stdout)
	if text == "" {
		text = strings.TrimSpace(result.Stderr)
	}
	if text == "" {
		return "version unavailable"
	}
	return "version: " + singleLine(firstLine(text))
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
