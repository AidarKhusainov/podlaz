//go:build linux

package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/godbus/dbus/v5"
)

const (
	nmL3IntentSchema        = "podlaz.nm-owned-l3-intent.v1"
	nmL3IntentFile          = "nm-owned-l3-intent.json"
	nmL3AddFlags     uint32 = 0x22 // in-memory, block autoconnect
)

type nmL3Plan struct {
	Interface string
	MTU       uint32
	IPv4      string
	DNS       string
	Metric    uint32
}

// nmL3Intent is persisted BEFORE NM mutation and retained after any
// interrupted/ambiguous D-Bus operation. It grants no cleanup authority.
type nmL3Intent struct {
	Schema       string          `json:"schema"`
	Owner        string          `json:"owner"`
	BootID       string          `json:"boot_id"`
	SessionID    string          `json:"session_id"`
	UUID         string          `json:"uuid"`
	Interface    string          `json:"interface"`
	IPv4         string          `json:"ipv4"`
	DNS          string          `json:"dns"`
	Mark         uint32          `json:"mark"`
	MTU          uint32          `json:"mtu"`
	Metric       uint32          `json:"metric"`
	Stage        string          `json:"stage"`
	SettingsPath dbus.ObjectPath `json:"settings_path,omitempty"`
	ActivePath   dbus.ObjectPath `json:"active_path,omitempty"`
}

type nmL3Mutator interface {
	Add(context.Context, map[string]map[string]dbus.Variant) (dbus.ObjectPath, error)
	Activate(context.Context, dbus.ObjectPath) (dbus.ObjectPath, error)
}

type nmL3Stager struct{ session networkSessionStateStore }

func newNML3Stager(dir string, boot bootIDReader) nmL3Stager {
	return nmL3Stager{session: newNetworkSessionStateStore(dir, boot)}
}
func (s nmL3Stager) path() string { return filepath.Join(s.session.runtimeDir, nmL3IntentFile) }

// StageProtected is a future protected-session orchestration step, not wired
// into the current default backend. It does not hand off a TUN FD or start Xray.
// Only NM creates/configures link, main IPv4 default and DNS.
func (s nmL3Stager) StageProtected(ctx context.Context, sessionID string, mark uint32, plan nmL3Plan) (nmL3Intent, error) {
	if ctx == nil {
		return nmL3Intent{}, errors.New("missing NM activation context")
	}
	bus, err := connectNML3Bus(ctx)
	if err != nil {
		return nmL3Intent{}, err
	}
	defer bus.Close()
	return s.stageWith(ctx, sessionID, mark, plan, bus, nmL3NameUnoccupied, verifyNML3Barrier)
}

func (s nmL3Stager) stageWith(ctx context.Context, sessionID string, mark uint32, plan nmL3Plan, nm nmL3Mutator, checkName func(string) error, verifyBarrier func(context.Context, networkSessionProtection) error) (nmL3Intent, error) {
	if ctx == nil || nm == nil || checkName == nil || verifyBarrier == nil || mark == 0 || !networkSessionIDPattern.MatchString(sessionID) {
		return nmL3Intent{}, errors.New("incomplete NM TUN activation authority")
	}
	if err := ctx.Err(); err != nil {
		return nmL3Intent{}, err
	}
	if err := validateNML3Plan(plan); err != nil {
		return nmL3Intent{}, err
	}
	lock := s.session.mutationLock()
	lock.Lock()
	defer lock.Unlock()
	generation := nmGenerationFence{session: s.session}
	unlock, err := generation.lock()
	if err != nil {
		return nmL3Intent{}, err
	}
	defer unlock()
	if _, err := os.Lstat(s.path()); err == nil {
		return nmL3Intent{}, errors.New("NM activation intent already exists; exact recovery is required")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nmL3Intent{}, fmt.Errorf("inspect NM intent: %w", err)
	}
	if _, exists, err := generation.load(); err != nil {
		return nmL3Intent{}, err
	} else if exists {
		return nmL3Intent{}, errors.New("NM generation reservation already exists")
	}
	state, exists, err := s.session.loadLocked()
	if err != nil {
		return nmL3Intent{}, err
	}
	boot, err := s.session.currentBootID()
	if err != nil {
		return nmL3Intent{}, err
	}
	if !exists || state.BootID != boot || state.SessionID != sessionID ||
		state.Intent != networkSessionIntentResume || state.Request.Mode != "tun" ||
		state.Replacement != nil || state.Protection == nil ||
		state.Protection.State != networkSessionProtectionArmed ||
		state.Protection.CompositionVersion != privacyEnvelopeMarkedCompositionVersion ||
		state.Protection.TunInterface != plan.Interface || len(state.Protection.PreviousEgressMarks) != 0 {
		return nmL3Intent{}, errors.New("NM activation requires exact armed marked Network Session")
	}
	armed := false
	for _, m := range state.Protection.EgressMarks {
		if m == mark {
			armed = true
		}
	}
	if !armed {
		return nmL3Intent{}, errors.New("NM egress mark is not protected")
	}
	if err := checkName(plan.Interface); err != nil {
		return nmL3Intent{}, fmt.Errorf("NM name collision or inspection failure: %w", err)
	}
	if err := verifyBarrier(ctx, *state.Protection); err != nil {
		return nmL3Intent{}, fmt.Errorf("verify exact armed privacy barrier before NM mutation: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nmL3Intent{}, err
	}
	uuid, err := newNML3UUID()
	if err != nil {
		return nmL3Intent{}, err
	}
	intent := nmL3Intent{
		Schema: nmL3IntentSchema, Owner: networkSessionStateOwner, BootID: boot, SessionID: sessionID,
		UUID: uuid, Interface: plan.Interface, IPv4: plan.IPv4, DNS: plan.DNS, Mark: mark, MTU: plan.MTU, Metric: plan.Metric, Stage: "planned",
	}
	if err := s.persist(intent); err != nil {
		return nmL3Intent{}, err
	}
	// Once mutation starts, even a timeout/failed call may have applied.
	// Retain the exact durable intent and Privacy Envelope; never guess cleanup.
	settingsPath, err := nm.Add(ctx, nmL3Settings(uuid, plan))
	if err != nil {
		return intent, fmt.Errorf("add NM-only TUN connection: %w", err)
	}
	if !validNMObjectPath(settingsPath, "/org/freedesktop/NetworkManager/Settings/") {
		return intent, errors.New("ambiguous NM settings path after addition")
	}
	intent.SettingsPath, intent.Stage = settingsPath, "profile-added"
	if err := s.persist(intent); err != nil {
		return intent, err
	}
	if err := ctx.Err(); err != nil {
		return intent, err
	}
	activePath, err := nm.Activate(ctx, settingsPath)
	if err != nil {
		return intent, fmt.Errorf("activate NM-owned TUN: %w", err)
	}
	if !validNMObjectPath(activePath, "/org/freedesktop/NetworkManager/ActiveConnection/") {
		return intent, errors.New("ambiguous NM active connection after activation")
	}
	intent.ActivePath, intent.Stage = activePath, "activating"
	if err := s.persist(intent); err != nil {
		return intent, err
	}
	return intent, nil // "activating" is NOT confirmation of NM FULL or FD admission.
}

func (s nmL3Stager) persist(intent nmL3Intent) error {
	data, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	if err := atomicWritePrivateFile(s.path(), append(data, '\n')); err != nil {
		return fmt.Errorf("persist NM activation intent: %w", err)
	}
	return nil
}

func validateNML3Plan(p nmL3Plan) error {
	if err := validateNetworkSessionInterface(p.Interface); err != nil {
		return err
	}
	if p.MTU != 1500 || p.Metric == 0 || p.Metric > 1024 {
		return errors.New("invalid NM TUN MTU or route metric")
	}
	addr, err := netip.ParsePrefix(p.IPv4)
	if err != nil || !addr.Addr().Is4() || addr.Addr().IsUnspecified() || addr.Addr().IsMulticast() || addr.String() != p.IPv4 {
		return errors.New("invalid NM TUN IPv4 allocation")
	}
	dns, err := netip.ParseAddr(p.DNS)
	if err != nil || !dns.Is4() || dns.IsUnspecified() || dns.IsMulticast() || dns.IsLoopback() || dns.String() != p.DNS {
		return errors.New("invalid NM TUN DNS target")
	}
	return nil
}

func nmL3Settings(uuid string, plan nmL3Plan) map[string]map[string]dbus.Variant {
	prefix, _ := netip.ParsePrefix(plan.IPv4) // validated by caller
	v := dbus.MakeVariant
	return map[string]map[string]dbus.Variant{
		"connection": {
			"id": v("podlaz-nm-" + uuid), "uuid": v(uuid), "type": v("tun"),
			"interface-name": v(plan.Interface), "autoconnect": v(false),
		},
		"tun": {"mode": v(uint32(1)), "pi": v(false), "multi-queue": v(false),
			"owner": v("0"), "group": v("0")},
		"ipv4": {
			"method":       v("manual"),
			"address-data": v([]map[string]dbus.Variant{{"address": v(prefix.Addr().String()), "prefix": v(uint32(prefix.Bits()))}}),
			"route-data":   v([]map[string]dbus.Variant{{"dest": v("0.0.0.0"), "prefix": v(uint32(0))}}),
			"route-table":  v(uint32(254)), "route-metric": v(int64(plan.Metric)),
			"dns-data": v([]string{plan.DNS}), "dns-search": v([]string{"~."}),
			"dns-priority": v(int32(-30000)), "never-default": v(false),
		},
		"ipv6": {"method": v("disabled")},
	}
}

// This verifier checks the actual nftables rules, not just the durable
// session state. It is read-only and cannot arm/repair a missing barrier.
func verifyNML3Barrier(ctx context.Context, authority networkSessionProtection) error {
	plan, err := privacyEnvelopePlanFromAuthority(authority)
	if err != nil {
		return err
	}
	return (netexecutor.PrivacyEnvelopeExecutor{}).Verify(ctx, plan)
}

func nmL3NameUnoccupied(name string) error {
	links, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("inspect existing links: %w", err)
	}
	for _, link := range links {
		if link.Name == name {
			return errors.New("foreign interface occupies NM TUN name")
		}
	}
	return nil
}

func newNML3UUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate NM UUID: %w", err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	s := hex.EncodeToString(raw[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

type nmL3Bus struct {
	conn  *dbus.Conn
	owner string
}

func connectNML3Bus(ctx context.Context) (*nmL3Bus, error) {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("connect NM system D-Bus: %w", err)
	}
	var owner string
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, networkManagerBusName).Store(&owner); err != nil || !strings.HasPrefix(owner, ":") {
		conn.Close()
		return nil, errors.New("NetworkManager unique bus owner unavailable")
	}
	return &nmL3Bus{conn: conn, owner: owner}, nil
}
func (b *nmL3Bus) Close() { b.conn.Close() }

func (b *nmL3Bus) Add(ctx context.Context, settings map[string]map[string]dbus.Variant) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	var extra map[string]dbus.Variant
	err := b.conn.Object(b.owner, dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings")).CallWithContext(
		ctx, "org.freedesktop.NetworkManager.Settings.AddConnection2", 0, settings, nmL3AddFlags, map[string]dbus.Variant{},
	).Store(&path, &extra)
	if err != nil {
		return "", err
	}
	if err := b.sameOwner(ctx); err != nil {
		return "", err
	}
	return path, nil
}
func (b *nmL3Bus) Activate(ctx context.Context, path dbus.ObjectPath) (dbus.ObjectPath, error) {
	var active dbus.ObjectPath
	err := b.conn.Object(b.owner, nmManagerPath).CallWithContext(ctx,
		nmManagerInterface+".ActivateConnection", 0, path, dbus.ObjectPath("/"), dbus.ObjectPath("/"),
	).Store(&active)
	if err != nil {
		return "", err
	}
	if err := b.sameOwner(ctx); err != nil {
		return "", err
	}
	return active, nil
}
func (b *nmL3Bus) sameOwner(ctx context.Context) error {
	var owner string
	if err := b.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, networkManagerBusName).Store(&owner); err != nil || owner != b.owner {
		return errors.New("NetworkManager changed during activation")
	}
	return nil
}
