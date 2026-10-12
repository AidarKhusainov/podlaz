package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The private startup marker is diagnostic evidence, never networking authority.
const startupProgressFileName = "startup-progress"

type startupStage string

const (
	startupStagePrepared        startupStage = "startup-prepared"
	startupStageSerialized      startupStage = "startup-serialized"
	startupStageSessionLoad     startupStage = "session-state-load"
	startupStagePrivacy         startupStage = "privacy-reconcile"
	startupStageExactRecovery   startupStage = "exact-recovery"
	startupStageGenericRecovery startupStage = "generic-recovery"
	startupStageConnectReplay   startupStage = "connect-replay"
	startupStageTerminal        startupStage = "terminal-recovery"
	startupStageBootAutostart   startupStage = "boot-autostart"
	startupStageScan            startupStage = "startup-scan"
	startupStageListenerOpen    startupStage = "listener-opening"
	startupStageListenerServe   startupStage = "listener-serving"
)

func (stage startupStage) valid() bool {
	switch stage {
	case startupStagePrepared, startupStageSerialized, startupStageSessionLoad,
		startupStagePrivacy, startupStageExactRecovery, startupStageGenericRecovery,
		startupStageConnectReplay, startupStageTerminal, startupStageBootAutostart,
		startupStageScan, startupStageListenerOpen, startupStageListenerServe:
		return true
	default:
		return false
	}
}

type startupProgressRecorder struct {
	path       string
	invocation string
	pid        int
	started    time.Time
}

func newStartupProgressRecorder(runtimeDir string) *startupProgressRecorder {
	invocation := os.Getenv("INVOCATION_ID")
	if len(invocation) != 32 {
		invocation = "none"
	} else {
		for _, digit := range invocation {
			if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f') ||
				(digit >= 'A' && digit <= 'F')) {
				invocation = "none"
				break
			}
		}
	}
	progress := &startupProgressRecorder{
		path:       filepath.Join(runtimeDir, startupProgressFileName),
		invocation: invocation,
		pid:        os.Getpid(),
		started:    time.Now(),
	}
	progress.record(startupStagePrepared)
	return progress
}

func (progress *startupProgressRecorder) record(stage startupStage) {
	if progress == nil || !stage.valid() {
		return
	}
	// Best-effort, atomic, private replacement. No fsync: not persisted authority.
	file, err := os.CreateTemp(filepath.Dir(progress.path), ".startup-progress-*")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return
	}
	elapsed := int64(time.Since(progress.started).Seconds())
	if elapsed < 0 {
		elapsed = 0
	}
	_, err = fmt.Fprintf(file, "pid=%d\ninvocation=%s\nstage=%s\nelapsed_seconds=%d\n",
		progress.pid, progress.invocation, stage, elapsed)
	if closeErr := file.Close(); err != nil || closeErr != nil {
		return
	}
	_ = os.Rename(file.Name(), progress.path)
}
