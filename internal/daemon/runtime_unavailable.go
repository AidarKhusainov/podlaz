package daemon

import (
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const packagedRuntimeDir = "/usr/lib/podlaz/"

type runtimeUnavailableError struct {
	message string
	cause   error
}

func (e *runtimeUnavailableError) Error() string {
	if e == nil || e.message == "" {
		return "runtime unavailable"
	}
	return e.message
}

func (e *runtimeUnavailableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newRuntimeUnavailableError(component, detail string) error {
	return newRuntimeUnavailableErrorWithCause(component, detail, nil)
}

func newRuntimeUnavailableErrorWithCause(component, detail string, cause error) error {
	var b strings.Builder
	fmt.Fprintf(&b, "TUN mode cannot start because %s is unavailable.", component)
	if strings.TrimSpace(detail) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(detail))
	}
	b.WriteString("\nNo network changes were applied.")
	b.WriteString("\nRun: plz doctor")
	return &runtimeUnavailableError{message: b.String(), cause: cause}
}

func wrapRuntimeUnavailable(component string, err error) error {
	if err == nil {
		return nil
	}
	return newRuntimeUnavailableErrorWithCause(component, err.Error(), err)
}

func isRuntimeUnavailableError(err error) bool {
	var target *runtimeUnavailableError
	return errors.As(err, &target)
}

func resolveRuntimeExecutable(explicit, envName, defaultCommand, component string) (string, error) {
	path := strings.TrimSpace(explicit)
	if path == "" && envName != "" {
		path = strings.TrimSpace(os.Getenv(envName))
	}
	if path == "" {
		path = defaultCommand
	}
	if path == "" {
		return "", newRuntimeUnavailableError(component, "No executable path was configured.")
	}
	if strings.ContainsRune(path, os.PathSeparator) {
		info, err := os.Stat(path)
		if err != nil {
			return "", newRuntimeUnavailableError(component, fmt.Sprintf("Expected: %s\nReason: %v", path, err))
		}
		if info.IsDir() {
			return "", newRuntimeUnavailableError(component, fmt.Sprintf("Expected executable file, got directory: %s", path))
		}
		if info.Mode().Perm()&0o111 == 0 {
			return "", newRuntimeUnavailableError(component, fmt.Sprintf("Expected executable file: %s\nReason: file is not executable", path))
		}
		if err := validatePackagedRuntimeArchitecture(path, component); err != nil {
			return "", err
		}
		return path, nil
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", newRuntimeUnavailableError(component, fmt.Sprintf("Command %q was not found in PATH. Set %s to an executable path or install the full podlaz package dependencies.", path, envName))
	}
	return resolved, nil
}

func validatePackagedRuntimeArchitecture(path, component string) error {
	if runtime.GOOS != "linux" || !strings.HasPrefix(path, packagedRuntimeDir) {
		return nil
	}
	f, err := elf.Open(path)
	if err != nil {
		return newRuntimeUnavailableError(component, fmt.Sprintf("Expected Linux ELF executable: %s\nReason: %v", path, err))
	}
	defer f.Close()
	wantMachine, ok := expectedELFMachine(runtime.GOARCH)
	if !ok {
		return nil
	}
	if f.FileHeader.Machine != wantMachine {
		return newRuntimeUnavailableError(component, fmt.Sprintf("Expected %s helper for %s, got ELF machine %s at %s", component, runtime.GOARCH, f.FileHeader.Machine, path))
	}
	return nil
}

func expectedELFMachine(goarch string) (elf.Machine, bool) {
	switch goarch {
	case "amd64":
		return elf.EM_X86_64, true
	case "arm64":
		return elf.EM_AARCH64, true
	default:
		return 0, false
	}
}

func validateTunRuntimeDependencies() error {
	for _, dep := range []struct {
		command   string
		component string
	}{
		{command: "ip", component: "iproute2"},
		{command: "nft", component: "nftables"},
		{command: "resolvectl", component: "systemd-resolved"},
	} {
		if _, err := exec.LookPath(dep.command); err != nil {
			return newRuntimeUnavailableError(dep.component, fmt.Sprintf("Command %q was not found in PATH. Install the full podlaz package dependencies before running TUN mode.", dep.command))
		}
	}
	return nil
}
