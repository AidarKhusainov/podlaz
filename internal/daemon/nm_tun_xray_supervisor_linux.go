//go:build linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const nmOwnedXrayExecutable = "/usr/lib/podlaz/xray-nm-owned"

// nmConfinedXrayGeneration is an explicit, opt-in runtime consumer of the NM L3
// and generation authorities. The default TUN Connect path never invokes it.
// Its caller must validate the entire generated config with the pinned official
// Xray *before* staging NM L3 or mutating the Privacy Envelope.
type nmConfinedXrayGeneration struct {
	l3         nmL3Stager
	generation nmGenerationFence
}

// Run is intentionally one-shot. An admitted generation retains its durable
// deny fence even after a clean exit. There is no implicit rollover/recovery.
// The caller owns fd, config and the dedicated delegated cgroup. It must keep
// the Privacy Envelope armed until exact-owned cleanup is independently proven.
func (s nmConfinedXrayGeneration) Run(
	ctx context.Context, fd *os.File, kernel nmTunFDKernelIdentity,
	nm nmTunDBusAuthority, sessionID string, mark uint32,
	configPath, cgroupPath string,
) error {
	if ctx == nil || fd == nil || fd.Fd() < 3 || ctx.Err() != nil {
		return errors.New("NM Xray admission requires a live context and TUN descriptor")
	}
	if os.Geteuid() != 0 {
		return errors.New("NM TUN FD admission requires privileged broker")
	}
	if err := validateNMXrayBinaryAndConfig(configPath); err != nil {
		return err
	}
	cgroup, err := openNMGenerationCgroup(cgroupPath)
	if err != nil {
		return err
	}
	defer cgroup.Close()

	identity, err := dedicatedProxyCoreExecutionIdentity()
	if err != nil {
		return err
	}
	if !identity.DropCredentials || identity.UID == 0 || identity.GID == 0 {
		return errors.New("NM Xray admission cannot establish dedicated unprivileged identity")
	}
	if err := verifyNMXrayConfigChildAccess(configPath, identity.UID, identity.GID); err != nil {
		return fmt.Errorf("NM Xray config inaccessible to restricted child before reservation: %w", err)
	}

	// No generation may be reserved or admitted from inferred NM identities.
	// Compare D-Bus authority against the persisted in-memory L3 selection.
	intent, exists, err := s.l3.loadIntent()
	if err != nil {
		return err
	}
	if !exists || intent.Stage != "activating" ||
		intent.SessionID != sessionID || intent.Mark != mark ||
		intent.UUID != nm.UUID || intent.Interface != nm.Name ||
		intent.MTU != nm.MTU || intent.SettingsPath != nm.SettingsPath ||
		intent.ActivePath != nm.ActivePath || kernel.Name != intent.Interface ||
		kernel.MTU != int(intent.MTU) || !validNMObjectPath(nm.DevicePath, "/org/freedesktop/NetworkManager/Devices/") {
		return errors.New("NM Xray admission differs from durable L3 authority")
	}
	if err := s.l3.verifyActiveL3(ctx); err != nil {
		return fmt.Errorf("NM Xray admission requires verified active L3: %w", err)
	}
	reservation, err := s.generation.Reserve(ctx, int(fd.Fd()), sessionID, mark, kernel, nm)
	if err != nil {
		return err
	}
	// Reservation is now durable: every subsequent failure leaves a deny fence.
	if reservation.NM != nm || reservation.Kernel != kernel ||
		reservation.SessionID != intent.SessionID || reservation.EgressMark != intent.Mark {
		return errors.New("NM Xray durable generation changed during admission")
	}
	if err := s.l3.verifyActiveL3(ctx); err != nil {
		return fmt.Errorf("NM Xray L3 changed after reservation: %w", err)
	}
	if err := verifyNMOwnedTunReadOnlyPreflight(ctx, int(fd.Fd()), kernel, nm); err != nil {
		return fmt.Errorf("NM Xray FD changed before handoff: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	kernelAuthority, err := json.Marshal(kernel)
	if err != nil {
		return fmt.Errorf("encode exact NM Xray FD authority: %w", err)
	}
	cmd := exec.Command("/usr/bin/podlazd", nmOwnedRestrictedLauncherArg, configPath)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "XRAY_TUN_FD=3",
		"PODLAZ_NM_KERNEL_IDENTITY=" + string(kernelAuthority)}
	cmd.ExtraFiles = []*os.File{fd}
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, Pdeathsig: syscall.SIGKILL,
		UseCgroupFD: true, CgroupFD: int(cgroup.Fd()),
	}
	// UseCgroupFD attaches atomically at clone; never Start-then-move PID.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start confined official NM Xray generation: %w", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var processErr error
	select {
	case processErr = <-waited:
	case <-ctx.Done():
		// Quiescence is mandatory even if context is already cancelled.
		if err := killNMGenerationCgroup(cgroup); err != nil {
			return fmt.Errorf("NM Xray cohort shutdown unproven: %w", err)
		}
		processErr = <-waited
	}
	// The tracked process may have exited while descendants still hold copies
	// of the TUN FD. Kill the whole cohort and independently verify emptiness.
	if err := killNMGenerationCgroup(cgroup); err != nil {
		return fmt.Errorf("NM Xray cohort revocation unproven: %w", err)
	}
	if err := waitNMGenerationCgroupEmpty(cgroup, 5*time.Second); err != nil {
		return fmt.Errorf("NM Xray cohort quiescence unproven: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if processErr != nil {
		return fmt.Errorf("official NM Xray generation exited: %w", processErr)
	}
	return nil
}

func validateNMXrayBinaryAndConfig(configPath string) error {
	binary, err := os.Lstat(nmOwnedXrayExecutable)
	if err != nil {
		return fmt.Errorf("official NM Xray binary unavailable: %w", err)
	}
	binaryStat, ok := binary.Sys().(*syscall.Stat_t)
	if !ok || binaryStat.Uid != 0 || binary.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 ||
		!binary.Mode().IsRegular() || binary.Mode().Perm()&0o111 == 0 ||
		binary.Mode().Perm()&0o022 != 0 {
		return errors.New("official NM Xray binary has unsafe type or permissions")
	}
	info, err := os.Lstat(configPath)
	if err != nil {
		return fmt.Errorf("NM Xray generated config unavailable: %w", err)
	}
	configStat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || configStat.Uid != 0 ||
		!info.Mode().IsRegular() || info.Size() <= 0 ||
		info.Mode().Perm()&0o007 != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("NM Xray generated config has unsafe type or permissions")
	}
	return nil
}

func openNMGenerationCgroup(path string) (*os.File, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("NM Xray generation requires absolute delegated cgroup path")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open NM Xray generation cgroup: %w", err)
	}
	cgroup := os.NewFile(uintptr(fd), path)
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil || uint64(fs.Type) != uint64(unix.CGROUP2_SUPER_MAGIC) {
		cgroup.Close()
		return nil, errors.New("NM Xray admission requires a real cgroup v2 directory")
	}
	if err := verifyNMGenerationCgroupEmpty(cgroup); err != nil {
		cgroup.Close()
		return nil, fmt.Errorf("NM Xray cohort must be empty before admission: %w", err)
	}
	return cgroup, nil
}

func killNMGenerationCgroup(cgroup *os.File) error {
	fd, err := unix.Openat(int(cgroup.Fd()), "cgroup.kill", unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	_, err = unix.Write(fd, []byte("1"))
	return err
}

func verifyNMGenerationCgroupEmpty(cgroup *os.File) error {
	fd, err := unix.Openat(int(cgroup.Fd()), "cgroup.events", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "cgroup.events")
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return errors.New("untrusted NM Xray cgroup event evidence")
	}
	populated := -1
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "populated" {
			value, err := strconv.Atoi(fields[1])
			if err != nil || populated != -1 {
				return errors.New("invalid NM Xray cgroup populated evidence")
			}
			populated = value
		}
	}
	if populated != 0 {
		return errors.New("NM Xray cgroup is populated or cannot be proven empty")
	}
	return nil
}

// Wait after cgroup.kill: process exit and cgroup membership accounting are
// asynchronous. Never turn a timeout into assumed quiescence.
func waitNMGenerationCgroupEmpty(cgroup *os.File, timeout time.Duration) error {
	if timeout <= 0 {
		return errors.New("NM Xray quiescence timeout is invalid")
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for {
		if err := verifyNMGenerationCgroupEmpty(cgroup); err == nil {
			return nil
		}
		select {
		case <-deadline.C:
			return errors.New("NM Xray cgroup did not prove empty before deadline")
		case <-poll.C:
		}
	}
}
