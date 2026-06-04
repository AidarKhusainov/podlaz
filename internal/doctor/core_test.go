package doctor

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

func TestRunCoreReportsExecutableAndVersion(t *testing.T) {
	xrayPath := filepath.Join(t.TempDir(), "xray")
	report := RunCore(context.Background(), CoreOptions{
		XrayPath: xrayPath,
		Stat:     fakeExecutableStat,
		Runner: fakeRunner{commands: map[string]fakeCommand{
			"xray version": {stdout: "Xray 25.6.1 (Xray, Penetrates Everything.)"},
		}},
	})

	assertCheckOrder(t, report, []string{"xray", "xray-version", "config-test"})
	assertCheck(t, report, "xray", SeverityOK, xrayPath+" is executable")
	assertCheck(t, report, "xray-version", SeverityOK, "Xray 25.6.1")
	assertCheck(t, report, "config-test", SeverityWarning, "not checked")
	if report.HasFailures() {
		t.Fatal("valid executable and version output must not fail core diagnostics")
	}
}

func TestRunCoreFailsForMissingBinary(t *testing.T) {
	xrayPath := filepath.Join(t.TempDir(), "missing-xray")
	report := RunCore(context.Background(), CoreOptions{XrayPath: xrayPath})

	assertCheck(t, report, "xray", SeverityFail, "does not exist")
	assertCheck(t, report, "xray-version", SeverityWarning, "not checked")
	assertCheck(t, report, "config-test", SeverityWarning, "not checked")
	if !report.HasFailures() {
		t.Fatal("missing xray binary must fail core diagnostics")
	}
}

func TestRunCoreFailsForNonExecutableBinary(t *testing.T) {
	xrayPath := filepath.Join(t.TempDir(), "xray")
	report := RunCore(context.Background(), CoreOptions{
		XrayPath: xrayPath,
		Stat:     fakeNonExecutableStat,
	})

	assertCheck(t, report, "xray", SeverityFail, "is not executable")
	assertCheck(t, report, "xray-version", SeverityWarning, "not checked")
	assertCheck(t, report, "config-test", SeverityWarning, "not checked")
}

func TestRunCoreFailsWhenVersionCommandFails(t *testing.T) {
	xrayPath := filepath.Join(t.TempDir(), "xray")
	report := RunCore(context.Background(), CoreOptions{
		XrayPath: xrayPath,
		Stat:     fakeExecutableStat,
		Runner: fakeRunner{commands: map[string]fakeCommand{
			"xray version": {
				stderr:   "bad flag token=secret",
				exitCode: 2,
				err:      errors.New("exit status 2"),
			},
		}},
	})

	assertCheck(t, report, "xray", SeverityOK, "is executable")
	assertCheck(t, report, "xray-version", SeverityFail, "xray version failed")
	if !report.HasFailures() {
		t.Fatal("failing xray version command must fail core diagnostics")
	}
}

func fakeExecutableStat(string) (fs.FileInfo, error) {
	return fakeFileInfo{mode: 0o755}, nil
}

func fakeNonExecutableStat(string) (fs.FileInfo, error) {
	return fakeFileInfo{mode: 0o644}, nil
}

type fakeFileInfo struct {
	mode  fs.FileMode
	isDir bool
}

func (f fakeFileInfo) Name() string       { return "xray" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.isDir }
func (f fakeFileInfo) Sys() any           { return nil }
