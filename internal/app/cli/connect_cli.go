package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

type connectRunner func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error)
type disconnectRunner func(context.Context) (api.LifecycleResponse, error)

func runConnectCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printConnectHelp(stdout)
		return nil
	}
	selector, err := parseCanonicalConnectArgs(args)
	if err != nil {
		return err
	}

	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}
	p, err := resolveIntentProfile(store, selector)
	if err != nil {
		return profileCommandError(err)
	}
	if err := validateCanonicalVPNProfile(p); err != nil {
		return err
	}

	if currentSessionSatisfiesCanonicalIntent(ctx, p, opts) {
		renderConnectResponse(stdout, p, api.LifecycleResponse{Connection: "active", Mode: planner.ModeTun})
		return nil
	}

	response, err := runConnectWithHandoff(ctx, p, planner.ModeTun, api.HandoffReplacePodlaz, opts)
	if err != nil {
		return lifecycleCommandError(err)
	}
	if response.Connection != "active" || response.Mode != planner.ModeTun {
		return fmt.Errorf("unable to connect: daemon did not publish a verified full VPN session")
	}
	renderConnectResponse(stdout, p, response)
	return nil
}

func runProxyConnectCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printDebugProxyHelp(stdout)
		return nil
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return usageError("debug proxy requires exactly one profile")
	}
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}
	p, err := store.Resolve(args[0])
	if err != nil {
		return profileCommandError(err)
	}
	if err := validateConnectProfile(p, planner.ModeProxyOnly); err != nil {
		return err
	}
	response, err := runConnectWithHandoff(ctx, p, planner.ModeProxyOnly, api.HandoffBlock, opts)
	if err != nil {
		return lifecycleCommandError(err)
	}
	fmt.Fprintln(stdout, "Connected with reduced protection")
	fmt.Fprintf(stdout, "Profile: %s\n", render.Redact(p.Name))
	fmt.Fprintln(stdout, "Protection: Proxy only")
	return nil
}

func runDisconnectCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printDisconnectHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		return usageError("disconnect does not accept arguments")
	}

	response, err := runDisconnect(ctx, opts)
	if err != nil {
		return lifecycleCommandError(err)
	}
	renderDisconnectResponse(stdout, response)
	return nil
}

func parseCanonicalConnectArgs(args []string) (string, error) {
	var selector string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return "", usageError("unsupported connect argument %q", arg)
		}
		if selector != "" {
			return "", usageError("connect accepts at most one profile")
		}
		selector = arg
	}
	return selector, nil
}

func resolveIntentProfile(store profile.Store, selector string) (profile.Profile, error) {
	if strings.TrimSpace(selector) != "" {
		return store.Resolve(selector)
	}
	return store.ResolveSelected()
}

func validateCanonicalVPNProfile(p profile.Profile) error {
	if err := validateConnectProfile(p, planner.ModeTun); err != nil {
		if validateConnectProfile(p, planner.ModeProxyOnly) == nil {
			name := render.Redact(p.Name)
			return fmt.Errorf("unable to connect\n\nprofile %q currently supports Proxy only; no network changes were made\n\nrun:\n  podlaz debug proxy %q", name, name)
		}
		return err
	}
	return nil
}

func currentSessionSatisfiesCanonicalIntent(ctx context.Context, p profile.Profile, opts options) bool {
	report, _ := runProductStatus(ctx, opts)
	return report.Connection == "active" &&
		report.Mode == planner.ModeTun &&
		report.ProfileID == p.ID &&
		!report.ProductReconnecting &&
		!statusCommandShouldFail(report)
}

func validateConnectProfile(p profile.Profile, mode string) error {
	if err := profile.Validate(p); err != nil {
		return err
	}
	if err := planner.ValidateXrayConnectProfile(p, mode); err != nil {
		return err
	}
	return nil
}

func runConnect(ctx context.Context, p profile.Profile, mode string, opts options) (api.LifecycleResponse, error) {
	return runConnectWithHandoff(ctx, p, mode, api.HandoffBlock, opts)
}

func runConnectWithHandoff(ctx context.Context, p profile.Profile, mode string, handoff string, opts options) (api.LifecycleResponse, error) {
	req := api.ConnectRequest{Mode: mode, Profile: profileSnapshot(p), Handoff: api.NormalizeHandoffPolicy(handoff)}
	if opts.connect != nil {
		return opts.connect(ctx, req)
	}
	return (client.LifecycleClient{}).Connect(ctx, req)
}

func runDisconnect(ctx context.Context, opts options) (api.LifecycleResponse, error) {
	if opts.disconnect != nil {
		return opts.disconnect(ctx)
	}
	return (client.LifecycleClient{}).Disconnect(ctx)
}

func lifecycleCommandError(err error) error {
	if client.IsDaemonUnavailable(err) {
		return exitError{code: 5, err: errors.New(client.UnavailableMessage(err))}
	}
	return err
}

func renderConnectResponse(stdout io.Writer, p profile.Profile, _ api.LifecycleResponse) {
	fmt.Fprintln(stdout, "Connected")
	fmt.Fprintf(stdout, "Profile: %s\n", render.Redact(p.Name))
	fmt.Fprintln(stdout, "Protection: Active")
}

func renderDisconnectResponse(stdout io.Writer, _ api.LifecycleResponse) {
	fmt.Fprintln(stdout, "Disconnected")
}

func profileSnapshot(p profile.Profile) api.ProfileSnapshot {
	return api.ProfileSnapshot{
		ID:               p.ID,
		Name:             p.Name,
		Source:           string(p.Source),
		Engine:           string(p.Engine),
		Server:           p.Server,
		Port:             p.Port,
		Protocol:         p.Protocol,
		UserIdentity:     p.UserIdentity,
		Transport:        p.Transport,
		Security:         p.Security,
		Encryption:       p.Encryption,
		Flow:             p.Flow,
		ServerName:       p.ServerName,
		ALPN:             p.ALPN,
		Fingerprint:      p.Fingerprint,
		Path:             p.Path,
		HostHeader:       p.HostHeader,
		ServiceName:      p.ServiceName,
		RealityPublicKey: p.RealityPublicKey,
		RealityShortID:   p.RealityShortID,
		RealitySpiderX:   p.RealitySpiderX,
	}
}

func printConnectHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz connect [profile]

Connect the explicit profile once, or the selected profile when no argument is
given. The normal connection is always a full VPN/TUN connection. Repeating the
same healthy intent is a no-op; replacing another exact Podlaz-owned TUN session
uses the protected replacement lifecycle automatically. Foreign or ambiguous
state is never adopted or removed to make the connection succeed.
`)
}

func printDisconnectHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz disconnect

Disconnect the Podlaz session. Repeating disconnect while Podlaz is already
conclusively inactive succeeds without changing foreign network state.
`)
}
