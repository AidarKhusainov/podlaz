//go:build linux

package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const nmOwnedRestrictedLauncherArg = "--nm-xray-restricted-exec"

// RunNMOwnedRestrictedXrayChild is invoked only by the cgroup-bound, root
// broker via the installed podlazd executable. It never handles a CLI request.
// The explicit child entrypoint permits dropping ALL unwanted capability
// bounding bits before execve, which SysProcAttr.Credential cannot do.
func RunNMOwnedRestrictedXrayChild(configPath string) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("NM Xray restricted child requires privileged broker")
	}
	if len(os.Args) != 3 || os.Args[1] != nmOwnedRestrictedLauncherArg ||
		os.Args[2] != configPath || os.Getenv("XRAY_TUN_FD") != "3" {
		return errors.New("invalid NM Xray restricted child invocation")
	}
	fd := os.NewFile(3, "inherited NM TUN")
	defer fd.Close()
	ifr, err := unix.NewIfreq("")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(int(fd.Fd()), unix.TUNGETIFF, ifr); err != nil ||
		ifr.Uint16()&(unix.IFF_TUN|unix.IFF_NO_PI) != unix.IFF_TUN|unix.IFF_NO_PI ||
		ifr.Uint16()&(unix.IFF_MULTI_QUEUE|unix.IFF_TAP|unix.IFF_VNET_HDR) != 0 {
		return errors.New("NM Xray restricted child did not receive a native single-queue TUN FD")
	}
	var expected nmTunFDKernelIdentity
	encoded := os.Getenv("PODLAZ_NM_KERNEL_IDENTITY")
	if len(encoded) == 0 || len(encoded) > 256 ||
		json.Unmarshal([]byte(encoded), &expected) != nil {
		return errors.New("NM Xray child lacks exact kernel generation identity")
	}
	if err := verifyNMOwnedTunFDKernelIdentity(3, expected); err != nil {
		return fmt.Errorf("NM Xray child FD kernel identity changed: %w", err)
	}
	if err := validateNMXrayBinaryAndConfig(configPath); err != nil {
		return err
	}
	identity, err := dedicatedProxyCoreExecutionIdentity()
	if err != nil {
		return err
	}
	if !identity.DropCredentials || identity.UID <= 0 || identity.GID <= 0 {
		return errors.New("NM Xray restricted child has no dedicated identity")
	}
	if err := verifyNMXrayConfigChildAccess(configPath, identity.UID, identity.GID); err != nil {
		return errors.New("NM Xray restricted child cannot access generated config")
	}
	// prctl/capset are per-thread; the same OS thread must perform the
	// entire privilege transition and the subsequent execve.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := restrictNMOwnedXrayCapabilities(identity.UID, identity.GID); err != nil {
		return fmt.Errorf("NM Xray capability isolation failed: %w", err)
	}
	// The original inherited FD 3 is preserved through exec. Avoid passing any
	// user-supplied environment or alternative FD selection variable to Xray.
	return syscall.Exec(nmOwnedXrayExecutable,
		[]string{nmOwnedXrayExecutable, "run", "-config", configPath},
		[]string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "XRAY_TUN_FD=3"})
}

func restrictNMOwnedXrayCapabilities(uid, gid int) error {
	lastBytes, err := os.ReadFile("/proc/sys/kernel/cap_last_cap")
	if err != nil {
		return err
	}
	last, err := strconv.Atoi(strings.TrimSpace(string(lastBytes)))
	if err != nil || last < unix.CAP_NET_RAW || last >= 64 {
		return errors.New("unsupported Linux capability inventory")
	}
	if err := unix.Prctl(unix.PR_CAPBSET_READ, uintptr(unix.CAP_NET_RAW), 0, 0, 0); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, uintptr(unix.PR_CAP_AMBIENT_CLEAR_ALL), 0, 0, 0); err != nil {
		return err
	}
	for cap := 0; cap <= last; cap++ {
		if cap == unix.CAP_NET_RAW {
			continue
		}
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, uintptr(cap), 0, 0, 0); err != nil {
			return err
		}
	}
	if err := unix.Prctl(unix.PR_SET_KEEPCAPS, 1, 0, 0, 0); err != nil {
		return err
	}
	if err := syscall.Setgroups([]int{}); err != nil {
		return err
	}
	if err := syscall.Setresgid(gid, gid, gid); err != nil {
		return err
	}
	if err := syscall.Setresuid(uid, uid, uid); err != nil {
		return err
	}
	capMask := uint32(1) << uint32(unix.CAP_NET_RAW%32)
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	data[unix.CAP_NET_RAW/32] = unix.CapUserData{
		Effective: capMask, Permitted: capMask, Inheritable: capMask,
	}
	if err := unix.Capset(&header, &data[0]); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, uintptr(unix.PR_CAP_AMBIENT_RAISE), uintptr(unix.CAP_NET_RAW), 0, 0); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	return nil
}
