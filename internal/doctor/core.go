package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

const SourceLocalCore = "local core"

// CoreOptions controls read-only core binary diagnostics.
type CoreOptions struct {
	Runner   CommandRunner
	Stat     func(string) (fs.FileInfo, error)
	XrayPath string
}

// RunCore validates a local Xray binary without starting a long-running core process.
func RunCore(ctx context.Context, opts CoreOptions) Report {
	runner := opts.Runner
	if runner == nil {
		runner = OSRunner{}
	}
	stat := opts.Stat
	if stat == nil {
		stat = os.Stat
	}

	xrayPath := strings.TrimSpace(opts.XrayPath)
	checks := make([]Check, 0, 3)

	if xrayPath == "" {
		checks = append(checks,
			Check{Name: "xray", Severity: SeverityFail, Message: "--xray <path> is required for doctor --core in v0.1"},
			notChecked("xray-version"),
			notChecked("config-test"),
		)
		return Report{Source: SourceLocalCore, Checks: checks}
	}

	info, err := stat(xrayPath)
	if err != nil {
		checks = append(checks,
			Check{Name: "xray", Severity: SeverityFail, Message: xrayStatFailureMessage(xrayPath, err)},
			notChecked("xray-version"),
			notChecked("config-test"),
		)
		return Report{Source: SourceLocalCore, Checks: checks}
	}
	if info.IsDir() {
		checks = append(checks,
			Check{Name: "xray", Severity: SeverityFail, Message: fmt.Sprintf("%s is a directory; provide the Xray executable file", xrayPath)},
			notChecked("xray-version"),
			notChecked("config-test"),
		)
		return Report{Source: SourceLocalCore, Checks: checks}
	}
	if info.Mode().Perm()&0o111 == 0 {
		checks = append(checks,
			Check{Name: "xray", Severity: SeverityFail, Message: fmt.Sprintf("%s is not executable; set execute permission or provide a different Xray binary", xrayPath)},
			notChecked("xray-version"),
			notChecked("config-test"),
		)
		return Report{Source: SourceLocalCore, Checks: checks}
	}

	checks = append(checks, Check{Name: "xray", Severity: SeverityOK, Message: fmt.Sprintf("%s is executable", xrayPath)})
	checks = append(checks, xrayVersion(ctx, runner, xrayPath))
	checks = append(checks, notChecked("config-test"))
	return Report{Source: SourceLocalCore, Checks: checks}
}

func xrayVersion(ctx context.Context, runner CommandRunner, xrayPath string) Check {
	result, err := runCommand(ctx, runner, xrayPath, "version")
	if commandSucceeded(result, err) {
		version := firstOutputLine(result)
		if version == "" {
			return Check{Name: "xray-version", Severity: SeverityWarning, Message: "xray version command completed without output"}
		}
		return Check{Name: "xray-version", Severity: SeverityOK, Message: version}
	}
	return Check{Name: "xray-version", Severity: SeverityFail, Message: "xray version failed: " + commandFailureMessage(result, err)}
}

func firstOutputLine(result CommandResult) string {
	for _, line := range strings.Split(result.Stdout+"\n"+result.Stderr, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return singleLine(line)
		}
	}
	return ""
}

func xrayStatFailureMessage(path string, err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("%s does not exist; install Xray or pass the correct --xray path", path)
	}
	if errors.Is(err, os.ErrPermission) {
		return fmt.Sprintf("cannot inspect %s: permission denied", path)
	}
	return fmt.Sprintf("cannot inspect %s: %v", path, err)
}

func notChecked(name string) Check {
	return Check{Name: name, Severity: SeverityWarning, Message: "not checked"}
}
