package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	bootAutostartNetworkReadinessTimeout  = 45 * time.Second
	bootAutostartNetworkReadinessInterval = 500 * time.Millisecond
)

var errBootAutostartNetworkNotReady = errors.New("boot network did not become ready within the bounded autostart window")

type bootNetworkReadinessWaiter struct {
	probe    func(context.Context) (bool, error)
	timeout  time.Duration
	interval time.Duration
}

func newBootNetworkReadinessWaiter() bootAutostartNetworkReadyFunc {
	probe := bootNetworkReadinessProbe{
		readRoutes: func() ([]byte, error) { return os.ReadFile("/proc/net/route") },
		interfaceAddrs: func(name string) ([]net.Addr, error) {
			iface, err := net.InterfaceByName(name)
			if err != nil {
				return nil, err
			}
			return iface.Addrs()
		},
	}
	waiter := bootAutostartReadinessWaiter{
		probe:    probe.Ready,
		timeout:  bootAutostartNetworkReadinessTimeout,
		interval: bootAutostartNetworkReadinessInterval,
	}
	return waiter.Wait
}

func (w bootAutostartReadinessWaiter) Wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if w.probe == nil {
		return errors.New("boot network readiness probe is unavailable")
	}
	timeout := w.timeout
	if timeout <= 0 {
		timeout = bootAutostartNetworkReadinessTimeout
	}
	interval := w.interval
	if interval <= 0 {
		interval = bootAutostartNetworkReadinessInterval
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		ready, _ := w.probe(waitCtx)
		if ready {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-waitCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errBootAutostartNetworkNotReady
		case <-timer.C:
		}
	}
}

type bootNetworkReadinessProbe struct {
	readRoutes     func() ([]byte, error)
	interfaceAddrs func(string) ([]net.Addr, error)
}

func (p bootNetworkReadinessProbe) Ready(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if p.readRoutes == nil || p.interfaceAddrs == nil {
		return false, errors.New("boot network readiness probe is incomplete")
	}
	routes, err := p.readRoutes()
	if err != nil {
		return false, err
	}
	interfaces, err := bootDefaultIPv4Interfaces(routes)
	if err != nil {
		return false, err
	}
	for _, iface := range interfaces {
		if strings.HasPrefix(iface, "podlaz") {
			continue
		}
		addrs, err := p.interfaceAddrs(iface)
		if err != nil {
			continue
		}
		if hasUsableBootIPv4(addrs) {
			return true, nil
		}
	}
	return false, nil
}

func bootDefaultIPv4Interfaces(data []byte) ([]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	seenHeader := false
	var interfaces []string
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if !seenHeader {
			seenHeader = true
			if fields[0] == "Iface" {
				continue
			}
		}
		if len(fields) < 4 || fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("parse default route flags: %w", err)
		}
		if flags&0x1 == 0 {
			continue
		}
		iface := strings.TrimSpace(fields[0])
		if iface == "" {
			continue
		}
		interfaces = append(interfaces, iface)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return interfaces, nil
}

func hasUsableBootIPv4(addrs []net.Addr) bool {
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		default:
			parsed, _, err := net.ParseCIDR(addr.String())
			if err == nil {
				ip = parsed
			}
		}
		if ip != nil && ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast() {
			return true
		}
	}
	return false
}
