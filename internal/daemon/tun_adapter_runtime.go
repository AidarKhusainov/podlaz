package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	tunAdapterPathEnv        = "PODLAZ_TUN2SOCKS_PATH"
	defaultTunAdapterCommand = "tun2socks"
)

type tunAdapterRuntimePlan struct {
	Binary        string
	TunDevice     string
	SOCKSEndpoint string
}

type tunAdapterHandle struct {
	cancel context.CancelFunc
	done   <-chan struct{}
}

var tunAdapterHandles sync.Map

func startTunAdapter(ctx context.Context, plan tunAdapterRuntimePlan) (*exec.Cmd, <-chan struct{}, context.CancelFunc, error) {
	device := strings.TrimSpace(plan.TunDevice)
	endpoint := strings.TrimSpace(plan.SOCKSEndpoint)
	if device == "" {
		return nil, nil, nil, errors.New("TUN adapter requires a TUN device")
	}
	if endpoint == "" {
		return nil, nil, nil, errors.New("TUN adapter requires a private SOCKS endpoint")
	}
	path, err := resolveTunAdapterPath(plan.Binary)
	if err != nil {
		return nil, nil, nil, err
	}
	cmdCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	deviceArg := "tun" + "://" + device
	proxyArg := "socks5" + "://" + endpoint
	cmd := exec.CommandContext(cmdCtx, path, "-device", deviceArg, "-proxy", proxyArg)
	cmd.Stdout = log.Writer()
	cmd.Stderr = log.Writer()
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, nil, fmt.Errorf("start TUN adapter: %w", err)
	}
	done := make(chan struct{})
	go func() {
		if err := cmd.Wait(); err != nil && cmdCtx.Err() == nil {
			log.Printf("podlaz: TUN adapter exited: %v", err)
		}
		close(done)
	}()
	if err := verifyTunAdapterStarted(done); err != nil {
		cancel()
		<-done
		return nil, nil, nil, err
	}
	return cmd, done, cancel, nil
}

func resolveTunAdapterPath(explicit string) (string, error) {
	path := strings.TrimSpace(explicit)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(tunAdapterPathEnv))
	}
	if path == "" {
		path = defaultTunAdapterCommand
	}
	if strings.ContainsRune(path, os.PathSeparator) {
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("resolve TUN adapter binary %s: %w", path, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("resolve TUN adapter binary %s: is a directory", path)
		}
		if info.Mode().Perm()&0o111 == 0 {
			return "", fmt.Errorf("resolve TUN adapter binary %s: not executable", path)
		}
		return path, nil
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve TUN adapter binary %q: %w; set %s to the tun2socks executable path", path, err, tunAdapterPathEnv)
	}
	return resolved, nil
}

func verifyTunAdapterStarted(done <-chan struct{}) error {
	if done == nil {
		return errors.New("missing TUN adapter completion channel")
	}
	select {
	case <-done:
		return errors.New("TUN adapter exited during startup verification")
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

func registerTunAdapter(m *XrayManager, cancel context.CancelFunc, done <-chan struct{}) {
	if m == nil || cancel == nil || done == nil {
		return
	}
	tunAdapterHandles.Store(m, tunAdapterHandle{cancel: cancel, done: done})
}

func stopRegisteredTunAdapter(m *XrayManager) error {
	if m == nil {
		return nil
	}
	value, ok := tunAdapterHandles.LoadAndDelete(m)
	if !ok {
		return nil
	}
	handle, ok := value.(tunAdapterHandle)
	if !ok {
		return nil
	}
	handle.cancel()
	select {
	case <-handle.done:
		return nil
	case <-time.After(defaultStopTimeout):
		return errors.New("TUN adapter did not stop before timeout")
	}
}
