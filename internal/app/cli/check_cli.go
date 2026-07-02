package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	profilecheck "github.com/AidarKhusainov/podlaz/internal/check"
	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

type checkRunner func(context.Context, profile.Profile, checkExecutionOptions) profileCheckReport

type checkExecutionOptions struct {
	Targets []profilecheck.Target
	Timeout time.Duration
}

type profileCheckReport struct {
	SchemaVersion string                     `json:"schema_version"`
	Status        string                     `json:"status"`
	Warnings      []string                   `json:"warnings"`
	Errors        []string                   `json:"errors"`
	Profile       profile.Profile            `json:"profile"`
	Mode          string                     `json:"mode"`
	Validation    profilecheck.ProbeResult   `json:"profile_validation"`
	Daemon        profilecheck.ProbeResult   `json:"daemon"`
	ProxyStartup  profilecheck.ProbeResult   `json:"proxy_startup"`
	ServerTCP     profilecheck.ProbeResult   `json:"server_tcp"`
	SOCKSEgress   profilecheck.ProbeResult   `json:"socks_egress"`
	HTTPEgress    profilecheck.ProbeResult   `json:"http_proxy_egress"`
	Services      []profilecheck.ProbeResult `json:"services"`
}

type checkBatchReport struct {
	SchemaVersion string               `json:"schema_version"`
	Status        string               `json:"status"`
	Warnings      []string             `json:"warnings"`
	Errors        []string             `json:"errors"`
	Mode          string               `json:"mode"`
	Concurrency   int                  `json:"concurrency"`
	Targets       []string             `json:"targets"`
	Profiles      []profileCheckReport `json:"profiles"`
}

func runCheckCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printCheckHelp(stdout)
		return nil
	}
	all, jsonOut, profileID, targetIDs, timeout, err := parseCheckFlags(args)
	if err != nil {
		return err
	}
	targets, err := profilecheck.SelectTargets(targetIDs)
	if err != nil {
		return usageError("%s", err.Error())
	}
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}
	execOpts := checkExecutionOptions{Targets: targets, Timeout: timeout}
	if all {
		profiles, err := store.List()
		if err != nil {
			return err
		}
		report := runAllProfileChecks(ctx, profiles, execOpts, opts)
		if jsonOut {
			err = writeJSON(stdout, report)
		} else {
			renderCheckBatch(stdout, report)
		}
		if err != nil {
			return err
		}
		if report.Status != "ok" {
			return exitError{code: 3, err: errors.New("one or more profile checks did not pass")}
		}
		return nil
	}
	p, err := store.Get(profileID)
	if err != nil {
		return profileCommandError(err)
	}
	report := runOneProfileCheck(ctx, p, execOpts, opts)
	if jsonOut {
		err = writeJSON(stdout, report)
	} else {
		renderProfileCheck(stdout, report)
	}
	if err != nil {
		return err
	}
	if report.Status != "ok" {
		return exitError{code: 3, err: errors.New("profile check did not pass")}
	}
	return nil
}

func parseCheckFlags(args []string) (bool, bool, string, []string, time.Duration, error) {
	var all, jsonOut bool
	var profileID string
	var targets []string
	timeout := profilecheck.DefaultTimeout
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, inline := cutFlagValue(arg)
		switch {
		case arg == "--all":
			all = true
		case arg == "--json":
			jsonOut = true
		case arg == "--target" || strings.HasPrefix(arg, "--target="):
			v, next, err := flagValue("check --target", args, i, value, inline)
			if err != nil {
				return false, false, "", nil, 0, err
			}
			targets = append(targets, v)
			i = next
		case arg == "--timeout" || strings.HasPrefix(arg, "--timeout="):
			v, next, err := flagValue("check --timeout", args, i, value, inline)
			if err != nil {
				return false, false, "", nil, 0, err
			}
			parsed, err := time.ParseDuration(v)
			if err != nil || parsed <= 0 {
				return false, false, "", nil, 0, usageError("check --timeout must be a positive duration such as 3s")
			}
			timeout = parsed
			i = next
		default:
			if strings.HasPrefix(arg, "-") {
				return false, false, "", nil, 0, usageError("unsupported check argument %q", arg)
			}
			if profileID != "" {
				return false, false, "", nil, 0, usageError("check accepts exactly one profile id unless --all is used")
			}
			profileID = arg
		}
	}
	if all && profileID != "" {
		return false, false, "", nil, 0, usageError("check --all does not accept a profile id")
	}
	if !all && profileID == "" {
		return false, false, "", nil, 0, usageError("check requires a profile id or --all")
	}
	return all, jsonOut, profileID, targets, timeout, nil
}

func runAllProfileChecks(ctx context.Context, profiles []profile.Profile, execOpts checkExecutionOptions, opts options) checkBatchReport {
	report := checkBatchReport{SchemaVersion: profilecheck.SchemaVersion, Status: "ok", Warnings: []string{}, Errors: []string{}, Mode: planner.ModeProxyOnly, Concurrency: profilecheck.DefaultBatchConcurrency, Targets: targetIDs(execOpts.Targets)}
	for _, p := range profiles {
		pr := runOneProfileCheck(ctx, p, execOpts, opts)
		report.Profiles = append(report.Profiles, pr)
		if pr.Status == "fail" {
			report.Status = "fail"
		} else if pr.Status != "ok" && report.Status == "ok" {
			report.Status = "degraded"
		}
	}
	return report
}

func runOneProfileCheck(ctx context.Context, p profile.Profile, execOpts checkExecutionOptions, opts options) profileCheckReport {
	if opts.check != nil {
		return redactProfileCheckReport(opts.check(ctx, p, execOpts))
	}
	return runDefaultProfileCheck(ctx, p, execOpts, opts)
}

func runDefaultProfileCheck(ctx context.Context, p profile.Profile, execOpts checkExecutionOptions, opts options) profileCheckReport {
	r := newProfileCheckReport(p, execOpts.Targets)
	if err := validateProfileForMode(p, planner.ModeProxyOnly); err != nil {
		r.Validation = profilecheck.Fail("profile_validation", "Profile validation", 0, err)
		r.Errors = append(r.Errors, err.Error())
		markRuntimeSkipped(&r, "profile is not renderable for proxy-only checks")
		return finalizeProfileCheckReport(r)
	}
	r.Validation = profilecheck.OK("profile_validation", "Profile validation", 0, "renderable for proxy-only")
	if p.Server != "" && p.Port != 0 {
		r.ServerTCP = runServerTCPProbe(ctx, opts.checkProbes, net.JoinHostPort(p.Server, strconv.Itoa(int(p.Port))), execOpts.Timeout)
	}
	statusReport, err := runDaemonStatus(ctx, opts)
	if err != nil {
		msg := err.Error()
		if client.IsDaemonUnavailable(err) {
			msg = client.UnavailableMessage(err)
		}
		r.Daemon = profilecheck.Fail("daemon", "Daemon", 0, errors.New(msg))
		r.Errors = append(r.Errors, msg)
		markProxySkipped(&r, "daemon is unavailable")
		return finalizeProfileCheckReport(r)
	}
	r.Daemon = profilecheck.OK("daemon", "Daemon", 0, "running; connection "+emptyAs(statusReport.Connection, "unknown"))
	if statusReport.Connection == "active" {
		msg := "connection already active; check did not replace or disconnect it"
		r.Warnings = append(r.Warnings, msg)
		markProxySkipped(&r, msg)
		return finalizeProfileCheckReport(r)
	}
	plan, err := planner.PlanProxyOnly(p)
	if err != nil {
		r.ProxyStartup = profilecheck.Fail("proxy_startup", "Proxy startup", 0, err)
		r.Errors = append(r.Errors, err.Error())
		markEgressSkipped(&r, "proxy-only plan could not be rendered")
		return finalizeProfileCheckReport(r)
	}
	lifecycleCtx, cancel := context.WithTimeout(ctx, lifecycleCheckTimeout(execOpts.Timeout))
	resp, err := runConnect(lifecycleCtx, p, planner.ModeProxyOnly, opts)
	cancel()
	if err != nil {
		msg := err.Error()
		if client.IsDaemonUnavailable(err) {
			msg = client.UnavailableMessage(err)
		}
		r.ProxyStartup = profilecheck.Fail("proxy_startup", "Proxy startup", 0, errors.New(msg))
		r.Errors = append(r.Errors, msg)
		markEgressSkipped(&r, "temporary proxy did not start")
		return finalizeProfileCheckReport(r)
	}
	r.ProxyStartup = profilecheck.OK("proxy_startup", "Proxy startup", 0, resp.Connection)
	endpoints := proxyEndpointsByProtocol(plan.Listeners)
	egressTarget := profilecheck.GenericEgressTarget()
	if ep, ok := endpoints["socks"]; ok {
		r.SOCKSEgress = renameProbe(runSOCKSEgressProbe(ctx, opts.checkProbes, ep, egressTarget, execOpts.Timeout), "socks_egress", "SOCKS egress")
	}
	if ep, ok := endpoints["http"]; ok {
		r.HTTPEgress = renameProbe(runHTTPProxyProbe(ctx, opts.checkProbes, ep, egressTarget, execOpts.Timeout), "http_proxy_egress", "HTTP proxy egress")
		r.Services = probeTargets(ctx, opts.checkProbes, ep, execOpts.Targets, execOpts.Timeout)
	} else {
		markEgressSkipped(&r, "HTTP proxy listener is not available")
	}
	if err := cleanupMatchingTemporaryProxy(context.Background(), opts, resp, execOpts.Timeout); err != nil {
		r.Warnings = append(r.Warnings, err.Error())
	}
	return finalizeProfileCheckReport(r)
}

func cleanupMatchingTemporaryProxy(ctx context.Context, opts options, started api.LifecycleResponse, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, lifecycleCheckTimeout(timeout))
	defer cancel()
	current, err := runDaemonStatus(ctx, opts)
	if err != nil {
		return fmt.Errorf("temporary proxy cleanup skipped: verify active connection failed: %w", err)
	}
	if current.Connection != "active" || current.Mode != planner.ModeProxyOnly || current.RuntimeConfigPath != started.RuntimeConfigPath || current.Proxy != started.Proxy {
		return errors.New("temporary proxy cleanup skipped: active connection does not match the check connection")
	}
	_, err = runDisconnect(ctx, opts)
	if err != nil {
		return fmt.Errorf("temporary proxy cleanup failed: %w", err)
	}
	return nil
}

func runServerTCPProbe(ctx context.Context, probes checkProbeRunner, address string, timeout time.Duration) profilecheck.ProbeResult {
	if probes.serverTCP != nil {
		return probes.serverTCP(ctx, address, timeout)
	}
	return profilecheck.MeasureTCP(ctx, address, timeout)
}

func runSOCKSEgressProbe(ctx context.Context, probes checkProbeRunner, endpoint profilecheck.Endpoint, target profilecheck.Target, timeout time.Duration) profilecheck.ProbeResult {
	if probes.socks != nil {
		return probes.socks(ctx, endpoint, target, timeout)
	}
	return profilecheck.ProbeHTTPSOverSOCKS(ctx, endpoint, target, timeout)
}

func runHTTPProxyProbe(ctx context.Context, probes checkProbeRunner, endpoint profilecheck.Endpoint, target profilecheck.Target, timeout time.Duration) profilecheck.ProbeResult {
	if probes.httpProxy != nil {
		return probes.httpProxy(ctx, endpoint, target, timeout)
	}
	return profilecheck.ProbeHTTPSOverHTTPProxy(ctx, endpoint, target, timeout)
}

func newProfileCheckReport(p profile.Profile, targets []profilecheck.Target) profileCheckReport {
	return profileCheckReport{SchemaVersion: profilecheck.SchemaVersion, Status: "ok", Warnings: []string{}, Errors: []string{}, Profile: profileForOutput(p), Mode: planner.ModeProxyOnly, Validation: profilecheck.Skipped("profile_validation", "Profile validation", "not run"), Daemon: profilecheck.Skipped("daemon", "Daemon", "not run"), ProxyStartup: profilecheck.Skipped("proxy_startup", "Proxy startup", "not run"), ServerTCP: profilecheck.Skipped("server_tcp", "Server TCP handshake", "not run"), SOCKSEgress: profilecheck.Skipped("socks_egress", "SOCKS egress", "not run"), HTTPEgress: profilecheck.Skipped("http_proxy_egress", "HTTP proxy egress", "not run"), Services: skippedServices(targets, "not run")}
}

func markRuntimeSkipped(r *profileCheckReport, detail string) {
	r.Daemon = profilecheck.Skipped("daemon", "Daemon", detail)
	markProxySkipped(r, detail)
	r.ServerTCP = profilecheck.Skipped("server_tcp", "Server TCP handshake", detail)
}
func markProxySkipped(r *profileCheckReport, detail string) {
	r.ProxyStartup = profilecheck.Skipped("proxy_startup", "Proxy startup", detail)
	markEgressSkipped(r, detail)
}
func markEgressSkipped(r *profileCheckReport, detail string) {
	r.SOCKSEgress = profilecheck.Skipped("socks_egress", "SOCKS egress", detail)
	r.HTTPEgress = profilecheck.Skipped("http_proxy_egress", "HTTP proxy egress", detail)
	r.Services = skippedServicesFromResults(r.Services, detail)
}

func probeTargets(ctx context.Context, probes checkProbeRunner, endpoint profilecheck.Endpoint, targets []profilecheck.Target, timeout time.Duration) []profilecheck.ProbeResult {
	out := make([]profilecheck.ProbeResult, 0, len(targets))
	for _, target := range targets {
		out = append(out, runHTTPProxyProbe(ctx, probes, endpoint, target, timeout))
	}
	return out
}

func skippedServices(targets []profilecheck.Target, detail string) []profilecheck.ProbeResult {
	out := make([]profilecheck.ProbeResult, 0, len(targets))
	for _, target := range targets {
		out = append(out, profilecheck.Skipped(target.ID, target.DisplayName, detail))
	}
	return out
}

func skippedServicesFromResults(results []profilecheck.ProbeResult, detail string) []profilecheck.ProbeResult {
	out := make([]profilecheck.ProbeResult, len(results))
	for i, result := range results {
		out[i] = profilecheck.Skipped(result.ID, result.Name, detail)
	}
	return out
}

func proxyEndpointsByProtocol(listeners []planner.Listener) map[string]profilecheck.Endpoint {
	out := make(map[string]profilecheck.Endpoint, len(listeners))
	for _, listener := range listeners {
		out[strings.ToLower(listener.Protocol)] = profilecheck.Endpoint{Protocol: strings.ToLower(listener.Protocol), Address: listener.Address, Port: listener.Port}
	}
	return out
}

func renameProbe(result profilecheck.ProbeResult, id, name string) profilecheck.ProbeResult {
	result.ID = id
	result.Name = name
	return result
}
func lifecycleCheckTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 10 * time.Second
	}
	if timeout+5*time.Second > 30*time.Second {
		return 30 * time.Second
	}
	return timeout + 5*time.Second
}

func finalizeProfileCheckReport(r profileCheckReport) profileCheckReport {
	r = redactProfileCheckReport(r)
	if r.Validation.Status == profilecheck.ResultFail || r.Daemon.Status == profilecheck.ResultFail || r.ProxyStartup.Status == profilecheck.ResultFail {
		r.Status = "fail"
		return r
	}
	if r.ServerTCP.Status != profilecheck.ResultOK || r.SOCKSEgress.Status != profilecheck.ResultOK || r.HTTPEgress.Status != profilecheck.ResultOK || len(r.Warnings) > 0 {
		r.Status = "degraded"
	}
	for _, service := range r.Services {
		if service.Status != profilecheck.ResultOK {
			r.Status = "degraded"
		}
	}
	return r
}

func redactProfileCheckReport(r profileCheckReport) profileCheckReport {
	r.Profile = profileForOutput(r.Profile)
	r.Warnings = redactStrings(r.Warnings)
	r.Errors = redactStrings(r.Errors)
	r.Validation = redactProbe(r.Validation)
	r.Daemon = redactProbe(r.Daemon)
	r.ProxyStartup = redactProbe(r.ProxyStartup)
	r.ServerTCP = redactProbe(r.ServerTCP)
	r.SOCKSEgress = redactProbe(r.SOCKSEgress)
	r.HTTPEgress = redactProbe(r.HTTPEgress)
	for i, service := range r.Services {
		r.Services[i] = redactProbe(service)
	}
	return r
}
func redactProbe(p profilecheck.ProbeResult) profilecheck.ProbeResult {
	p.ID = render.Redact(p.ID)
	p.Name = render.Redact(p.Name)
	p.Status = render.Redact(p.Status)
	p.Detail = render.Redact(p.Detail)
	p.Error = render.Redact(p.Error)
	return p
}
func redactStrings(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = render.Redact(v)
	}
	return out
}
func targetIDs(targets []profilecheck.Target) []string {
	out := make([]string, len(targets))
	for i, target := range targets {
		out[i] = target.ID
	}
	return out
}

func renderProfileCheck(w io.Writer, r profileCheckReport) {
	fmt.Fprintf(w, "Profile check: %s\nProfile ID: %s\nMode: %s\n", r.Profile.Name, r.Profile.ID, r.Mode)
	fmt.Fprintf(w, "Profile validation: %s\nDaemon: %s\nProxy startup: %s\nServer TCP handshake: %s\nSOCKS egress: %s\nHTTP proxy egress: %s\n", formatProbe(r.Validation), formatProbe(r.Daemon), formatProbe(r.ProxyStartup), formatProbe(r.ServerTCP), formatProbe(r.SOCKSEgress), formatProbe(r.HTTPEgress))
	for _, service := range r.Services {
		fmt.Fprintf(w, "%s: %s\n", service.Name, formatServiceProbe(service))
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintf(w, "Warnings: %d\n", len(r.Warnings))
		for _, warning := range r.Warnings {
			fmt.Fprintf(w, "- %s\n", warning)
		}
	}
	if len(r.Errors) > 0 {
		fmt.Fprintf(w, "Errors: %d\n", len(r.Errors))
		for _, err := range r.Errors {
			fmt.Fprintf(w, "- %s\n", err)
		}
	}
	fmt.Fprintf(w, "Result: %s\n", r.Status)
}

func renderCheckBatch(w io.Writer, r checkBatchReport) {
	fmt.Fprintln(w, "Profile connectivity checks")
	fmt.Fprintf(w, "Mode: %s\nConcurrency: %d\nTargets: %s\n", r.Mode, r.Concurrency, strings.Join(r.Targets, ", "))
	rows := make([][]string, 0, len(r.Profiles))
	for _, p := range r.Profiles {
		rows = append(rows, []string{p.Profile.ID, p.Profile.Name, p.Status, p.Validation.Status, p.ServerTCP.Status, p.ProxyStartup.Status, serviceSummary(p.Services)})
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "No profiles found.")
	} else {
		_ = writeTable(w, []string{"ID", "NAME", "RESULT", "VALIDATION", "SERVER_TCP", "PROXY", "SERVICES"}, rows)
	}
	fmt.Fprintf(w, "Result: %s\n", r.Status)
}

func formatProbe(p profilecheck.ProbeResult) string {
	if p.Status == profilecheck.ResultOK {
		if p.LatencyMS > 0 {
			return fmt.Sprintf("ok, %d ms", p.LatencyMS)
		}
		if p.Detail != "" {
			return "ok (" + p.Detail + ")"
		}
		return "ok"
	}
	if p.Status == profilecheck.ResultFail {
		if p.Error != "" {
			return "failed: " + p.Error
		}
		return "failed"
	}
	if p.Detail != "" {
		return p.Status + ": " + p.Detail
	}
	return p.Status
}
func formatServiceProbe(p profilecheck.ProbeResult) string {
	if p.Status == profilecheck.ResultOK {
		if p.LatencyMS > 0 {
			return fmt.Sprintf("reachable, %d ms", p.LatencyMS)
		}
		return "reachable"
	}
	if p.Status == profilecheck.ResultFail {
		if p.Error != "" {
			return "blocked or timed out: " + p.Error
		}
		return "blocked or timed out"
	}
	if p.Detail != "" {
		return p.Status + ": " + p.Detail
	}
	return p.Status
}
func serviceSummary(services []profilecheck.ProbeResult) string {
	ok, fail, skipped := 0, 0, 0
	for _, s := range services {
		switch s.Status {
		case profilecheck.ResultOK:
			ok++
		case profilecheck.ResultFail:
			fail++
		case profilecheck.ResultSkipped:
			skipped++
		}
	}
	return fmt.Sprintf("ok:%d fail:%d skipped:%d", ok, fail, skipped)
}

func printCheckHelp(w io.Writer) {
	fmt.Fprintf(w, `Usage:
  podlaz check <profile-id> [--target <target-id>] [--timeout <duration>] [--json]
  podlaz check --all [--target <target-id>] [--timeout <duration>] [--json]

Run explicit bounded proxy-only profile connectivity diagnostics. The command validates profile renderability, checks daemon state, starts temporary proxy-only Xray only through podlazd when safe, verifies local SOCKS/HTTP egress, runs documented service targets, and cleans up the temporary connection it started.

Safety:
  - does not mutate TUN, routes, DNS, nftables, firewall, or resolver files;
  - does not replace or disconnect an already active connection;
  - bounds every network probe by --timeout; default %s.

Targets: %s
`, profilecheck.DefaultTimeout, strings.Join(profilecheck.SupportedTargetIDs(), ", "))
}
