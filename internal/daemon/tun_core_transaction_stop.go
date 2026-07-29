package daemon

import "os/exec"

// stopStartedCoreForTransaction proves process quiescence without deleting the
// generated runtime config on an early stop error. The transaction rollback
// owns config removal after host cleanup and process absence both succeed.
func (m *XrayManager) stopStartedCoreForTransaction(cmd *exec.Cmd, done <-chan struct{}) error {
	m.mu.Lock()
	if m.cmd == cmd {
		m.stopping = true
	}
	m.mu.Unlock()
	return m.stopCoreProcess(cmd, done)
}
