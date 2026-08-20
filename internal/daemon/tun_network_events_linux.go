//go:build linux

package daemon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const (
	logindManagerPath      = dbus.ObjectPath("/org/freedesktop/login1")
	logindManagerInterface = "org.freedesktop.login1.Manager"
	logindPrepareForSleep  = "PrepareForSleep"

	netlinkHeaderLength = 16
	netlinkAlignment    = 4
)

type tunNetworkEventNotifyFunc func(tunRevalidationTrigger)
type tunNetworkEventReadyFunc func()
type tunNetworkEventSource func(context.Context, tunNetworkEventNotifyFunc, tunNetworkEventReadyFunc) error

func startTunNetworkEventSources(ctx context.Context, notify tunNetworkEventNotifyFunc) {
	if notify == nil {
		return
	}
	startE2ETunTerminalFailureTrigger(ctx, notify)
	go retryTunNetworkEventSource(ctx, "logind", runLogindSleepEvents, notify)
	go retryTunNetworkEventSource(ctx, "rtnetlink", runTunRtnetlinkEvents, notify)
}

func retryTunNetworkEventSource(ctx context.Context, name string, source tunNetworkEventSource, notify tunNetworkEventNotifyFunc) {
	retryTunNetworkEventSourceWithBackoff(ctx, name, source, notify, time.Second, 30*time.Second)
}

func retryTunNetworkEventSourceWithBackoff(ctx context.Context, name string, source tunNetworkEventSource, notify tunNetworkEventNotifyFunc, initialBackoff, maxBackoff time.Duration) {
	if source == nil || notify == nil {
		return
	}
	if initialBackoff <= 0 {
		initialBackoff = time.Second
	}
	if maxBackoff < initialBackoff {
		maxBackoff = initialBackoff
	}
	backoff := initialBackoff
	for ctx.Err() == nil {
		var readyOnce sync.Once
		ready := func() {
			readyOnce.Do(func() { notify(tunRevalidationTriggerSourceResync) })
		}
		err := source(ctx, notify, ready)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("podlazd: TUN network event source stopped source=%s; retrying", safeLogField(name))
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func runLogindSleepEvents(ctx context.Context, notify tunNetworkEventNotifyFunc, ready tunNetworkEventReadyFunc) error {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("connect system bus: %w", err)
	}
	defer conn.Close()

	options := []dbus.MatchOption{
		dbus.WithMatchObjectPath(logindManagerPath),
		dbus.WithMatchInterface(logindManagerInterface),
		dbus.WithMatchMember(logindPrepareForSleep),
	}
	if err := conn.AddMatchSignalContext(ctx, options...); err != nil {
		return fmt.Errorf("subscribe logind sleep signal: %w", err)
	}
	defer func() { _ = conn.RemoveMatchSignal(options...) }()

	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	if ready != nil {
		// Subscribe first, then schedule a fresh authoritative snapshot. Events
		// that happened while this source was disconnected cannot otherwise be
		// reconstructed from the edge-driven D-Bus stream.
		ready()
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case signal, ok := <-signals:
			if !ok {
				return errors.New("system D-Bus signal channel closed")
			}
			if signal == nil || signal.Name != logindManagerInterface+"."+logindPrepareForSleep || signal.Path != logindManagerPath || len(signal.Body) != 1 {
				continue
			}
			preparingForSleep, ok := signal.Body[0].(bool)
			if !ok {
				continue
			}
			if trigger, ok := tunSleepSignalTrigger(preparingForSleep); ok {
				notify(trigger)
			}
		}
	}
}

func runTunRtnetlinkEvents(ctx context.Context, notify tunNetworkEventNotifyFunc, ready tunNetworkEventReadyFunc) error {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("open rtnetlink socket: %w", err)
	}
	var closeOnce sync.Once
	closeSocket := func() { closeOnce.Do(func() { _ = unix.Close(fd) }) }
	defer closeSocket()

	groups := uint32(unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR | unix.RTMGRP_IPV4_ROUTE | unix.RTMGRP_IPV6_ROUTE)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: groups}); err != nil {
		return fmt.Errorf("bind rtnetlink event groups: %w", err)
	}
	stopCancellationWatcher := make(chan struct{})
	defer close(stopCancellationWatcher)
	go func() {
		select {
		case <-ctx.Done():
			closeSocket()
		case <-stopCancellationWatcher:
		}
	}()
	if ready != nil {
		// Binding installs the multicast subscription. Resync only after that
		// point so changes concurrent with the snapshot are represented either by
		// the snapshot itself or by a queued edge on the already-bound socket.
		ready()
	}

	buffer := make([]byte, 64*1024)
	for {
		n, _, err := unix.Recvfrom(fd, buffer, 0)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return fmt.Errorf("read rtnetlink event: %w", err)
		}
		triggers, err := tunNetlinkTriggers(buffer[:n])
		if err != nil {
			return err
		}
		for _, trigger := range triggers {
			notify(trigger)
		}
	}
}

func tunNetlinkTriggers(payload []byte) ([]tunRevalidationTrigger, error) {
	var triggers []tunRevalidationTrigger
	for offset := 0; offset < len(payload); {
		if len(payload)-offset < netlinkHeaderLength {
			return nil, errors.New("truncated rtnetlink message header")
		}
		length := int(binary.NativeEndian.Uint32(payload[offset : offset+4]))
		if length < netlinkHeaderLength || offset+length > len(payload) {
			return nil, fmt.Errorf("invalid rtnetlink message length %d", length)
		}
		messageType := binary.NativeEndian.Uint16(payload[offset+4 : offset+6])
		if trigger, ok := tunNetlinkTrigger(messageType); ok {
			triggers = append(triggers, trigger)
		}
		aligned := (length + netlinkAlignment - 1) &^ (netlinkAlignment - 1)
		if aligned <= 0 {
			return nil, errors.New("invalid rtnetlink message alignment")
		}
		offset += aligned
		if offset > len(payload) {
			return nil, errors.New("truncated aligned rtnetlink message")
		}
	}
	return triggers, nil
}

func tunNetlinkTrigger(messageType uint16) (tunRevalidationTrigger, bool) {
	switch messageType {
	case unix.RTM_NEWLINK, unix.RTM_DELLINK:
		return tunRevalidationTriggerLink, true
	case unix.RTM_NEWADDR, unix.RTM_DELADDR:
		return tunRevalidationTriggerAddress, true
	case unix.RTM_NEWROUTE, unix.RTM_DELROUTE:
		return tunRevalidationTriggerRoute, true
	default:
		return "", false
	}
}
