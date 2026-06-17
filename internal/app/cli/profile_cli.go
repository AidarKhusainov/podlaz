package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/engine"
	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
	"github.com/AidarKhusainov/tunwarden/internal/render"
)

func runProfileCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	_ = ctx
	if isHelp(args) {
		printProfileHelp(stdout)
		return nil
	}
	if len(args) == 0 {
		return usageError("profile requires a subcommand")
	}

	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}

	switch strings.ToLower(args[0]) {
	case "add":
		return runProfileAdd(store, args[1:], stdout)
	case "import":
		return runProfileImport(store, args[1:], stdout)
	case "list":
		return runProfileList(store, args[1:], stdout)
	case "show":
		return runProfileShow(store, args[1:], stdout)
	case "validate":
		return runProfileValidate(store, args[1:], stdout)
	case "delete":
		return runProfileDelete(store, args[1:], stdout)
	default:
		return usageError("unknown profile subcommand %q", args[0])
	}
}

func runProfileAdd(store profile.Store, args []string, stdout io.Writer) error {
	parsed, err := parseProfileAddArgs(args)
	if err != nil {
		return err
	}

	p := profile.NewManual(parsed.name, parsed.server, parsed.port, parsed.protocol)
	if err := store.Add(p); err != nil {
		return profileCommandError(err)
	}

	fmt.Fprintf(stdout, "Profile added: %s\n", p.ID)
	return nil
}

func runProfileImport(store profile.Store, args []string, stdout io.Writer) error {
	uri, err := parseProfileImportArgs(args)
	if err != nil {
		return err
	}

	p, warnings, err := profile.ImportShareURI(uri)
	if err != nil {
		return usageError("%s", err.Error())
	}
	if err := store.Add(p); err != nil {
		return profileCommandError(err)
	}

	out := profileForOutput(p)
	fmt.Fprintf(stdout, "Imported profile: %s\n", out.ID)
	fmt.Fprintf(stdout, "Name: %s\n", out.Name)
	if len(warnings) > 0 {
		fmt.Fprintf(stdout, "Warnings: %d\n", len(warnings))
		for _, warning := range warnings {
			fmt.Fprintf(stdout, "- %s\n", render.Redact(warning))
		}
	}
	return nil
}

func runProfileList(store profile.Store, args []string, stdout io.Writer) error {
	jsonOutput, err := parseOptionalJSON(args, "profile list")
	if err != nil {
		return err
	}

	profiles, err := store.List()
	if err != nil {
		return err
	}

	if jsonOutput {
		return writeJSON(stdout, okJSON(map[string]any{"profiles": profilesForOutput(profiles)}))
	}

	fmt.Fprintln(stdout, "ID        NAME   PROTOCOL  SERVER       PORT")
	for _, p := range profiles {
		out := profileForOutput(p)
		fmt.Fprintf(stdout, "%-9s %-6s %-9s %-12s %d\n", out.ID, out.Name, out.Protocol, out.Server, out.Port)
	}
	return nil
}

func runProfileShow(store profile.Store, args []string, stdout io.Writer) error {
	id, jsonOutput, err := parseProfileShowArgs(args)
	if err != nil {
		return err
	}

	p, err := store.Get(id)
	if err != nil {
		return profileCommandError(err)
	}

	out := profileForOutput(p)
	if jsonOutput {
		return writeJSON(stdout, okJSON(map[string]any{"profile": out}))
	}

	fmt.Fprintf(stdout, "ID: %s\n", out.ID)
	fmt.Fprintf(stdout, "Name: %s\n", out.Name)
	fmt.Fprintf(stdout, "Source: %s\n", out.Source)
	fmt.Fprintf(stdout, "Engine: %s\n", out.Engine)
	fmt.Fprintf(stdout, "Protocol: %s\n", out.Protocol)
	fmt.Fprintf(stdout, "Server: %s\n", out.Server)
	fmt.Fprintf(stdout, "Port: %d\n", out.Port)
	printOptionalProfileField(stdout, "User identity", out.UserIdentity)
	printOptionalProfileField(stdout, "Transport", out.Transport)
	printOptionalProfileField(stdout, "Security", out.Security)
	printOptionalProfileField(stdout, "Encryption", out.Encryption)
	printOptionalProfileField(stdout, "Flow", out.Flow)
	printOptionalProfileField(stdout, "Server name", out.ServerName)
	printOptionalProfileField(stdout, "ALPN", out.ALPN)
	printOptionalProfileField(stdout, "Fingerprint", out.Fingerprint)
	printOptionalProfileField(stdout, "Path", out.Path)
	printOptionalProfileField(stdout, "Host header", out.HostHeader)
	printOptionalProfileField(stdout, "Service name", out.ServiceName)
	printOptionalProfileField(stdout, "Reality public key", out.RealityPublicKey)
	printOptionalProfileField(stdout, "Reality short ID", out.RealityShortID)
	printOptionalProfileField(stdout, "Reality spider X", out.RealitySpiderX)
	return nil
}

func runProfileValidate(store profile.Store, args []string, stdout io.Writer) error {
	parsed, err := parseProfileValidateArgs(args)
	if err != nil {
		return err
	}

	p, err := store.Get(parsed.id)
	if err != nil {
		return profileCommandError(err)
	}

	validationErr := validateProfileForMode(p, parsed.mode)
	out := profileForOutput(p)
	if parsed.jsonOutput {
		status := "ok"
		valid := true
		errors := []string{}
		if validationErr != nil {
			status = "fail"
			valid = false
			errors = []string{render.Redact(validationErr.Error())}
		}
		if err := writeJSON(stdout, map[string]any{
			"schema_version": "v1",
			"status":         status,
			"warnings":       []string{},
			"errors":         errors,
			"profile":        out,
			"mode":           parsed.mode,
			"backend":        render.Redact(string(p.Engine)),
			"valid":          valid,
		}); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(stdout, "Profile validation")
		fmt.Fprintf(stdout, "Profile: %s\n", out.Name)
		fmt.Fprintf(stdout, "Profile ID: %s\n", out.ID)
		fmt.Fprintf(stdout, "Source: %s\n", out.Source)
		fmt.Fprintf(stdout, "Mode: %s\n", parsed.mode)
		fmt.Fprintf(stdout, "Backend: %s\n", render.Redact(string(p.Engine)))
		fmt.Fprintf(stdout, "Protocol: %s\n", out.Protocol)
		if validationErr == nil {
			fmt.Fprintln(stdout, "Status: valid")
		} else {
			fmt.Fprintln(stdout, "Status: invalid")
			fmt.Fprintf(stdout, "Reason: %s\n", render.Redact(validationErr.Error()))
		}
	}

	if validationErr != nil {
		return exitError{code: 3, err: validationErr}
	}
	return nil
}

func runProfileDelete(store profile.Store, args []string, stdout io.Writer) error {
	id, yes, err := parseProfileDeleteArgs(args)
	if err != nil {
		return err
	}
	if !yes {
		return usageError("profile delete requires --yes in this non-interactive v0.1 CLI")
	}
	if err := store.Delete(id); err != nil {
		return profileCommandError(err)
	}
	fmt.Fprintf(stdout, "Profile deleted: %s\n", id)
	return nil
}

type profileAddArgs struct {
	name     string
	server   string
	port     uint16
	protocol string
}

type profileValidateArgs struct {
	id         string
	mode       string
	jsonOutput bool
}

func parseProfileAddArgs(args []string) (profileAddArgs, error) {
	var parsed profileAddArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, hasInlineValue := cutFlagValue(arg)
		switch {
		case arg == "--name" || strings.HasPrefix(arg, "--name="):
			v, next, err := flagValue("profile add --name", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.name = v
			i = next
		case arg == "--server" || strings.HasPrefix(arg, "--server="):
			v, next, err := flagValue("profile add --server", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.server = v
			i = next
		case arg == "--protocol" || strings.HasPrefix(arg, "--protocol="):
			v, next, err := flagValue("profile add --protocol", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.protocol = v
			i = next
		case arg == "--port" || strings.HasPrefix(arg, "--port="):
			v, next, err := flagValue("profile add --port", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			port, err := strconv.ParseUint(v, 10, 16)
			if err != nil || port == 0 {
				return parsed, usageError("profile add --port must be a number between 1 and 65535")
			}
			parsed.port = uint16(port)
			i = next
		case arg == "--json":
			return parsed, usageError("profile add --json is not implemented")
		default:
			return parsed, usageError("unsupported profile add argument %q", arg)
		}
	}

	p := profile.NewManual(parsed.name, parsed.server, parsed.port, parsed.protocol)
	if err := profile.Validate(p); err != nil {
		return parsed, usageError("%s", err.Error())
	}
	return parsed, nil
}

func parseProfileImportArgs(args []string) (string, error) {
	var uri string
	for _, arg := range args {
		switch arg {
		case "--json":
			return "", usageError("profile import --json is not implemented")
		default:
			if strings.HasPrefix(arg, "-") {
				return "", usageError("unsupported profile import argument %q", arg)
			}
			if uri != "" {
				return "", usageError("profile import accepts exactly one share URI")
			}
			uri = arg
		}
	}
	if uri == "" {
		return "", usageError("profile import requires a share URI")
	}
	return uri, nil
}

func parseProfileShowArgs(args []string) (string, bool, error) {
	var id string
	var jsonOutput bool
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				return "", false, usageError("unsupported profile show argument %q", arg)
			}
			if id != "" {
				return "", false, usageError("profile show accepts exactly one profile id")
			}
			id = arg
		}
	}
	if id == "" {
		return "", false, usageError("profile show requires a profile id")
	}
	return id, jsonOutput, nil
}

func parseProfileValidateArgs(args []string) (profileValidateArgs, error) {
	parsed := profileValidateArgs{mode: planner.ModeProxyOnly}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, hasInlineValue := cutFlagValue(arg)
		switch {
		case arg == "--mode" || strings.HasPrefix(arg, "--mode="):
			v, next, err := flagValue("profile validate --mode", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.mode = strings.ToLower(strings.TrimSpace(v))
			i = next
		case arg == "--json":
			parsed.jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				return parsed, usageError("unsupported profile validate argument %q", arg)
			}
			if parsed.id != "" {
				return parsed, usageError("profile validate accepts exactly one profile id")
			}
			parsed.id = arg
		}
	}
	if parsed.id == "" {
		return parsed, usageError("profile validate requires a profile id")
	}
	switch parsed.mode {
	case planner.ModeProxyOnly, planner.ModeTun:
	default:
		return parsed, usageError("unsupported profile validate mode %q", parsed.mode)
	}
	return parsed, nil
}

func parseProfileDeleteArgs(args []string) (string, bool, error) {
	var id string
	var yes bool
	for _, arg := range args {
		switch arg {
		case "--yes":
			yes = true
		case "--json":
			return "", false, usageError("profile delete --json is not implemented")
		default:
			if strings.HasPrefix(arg, "-") {
				return "", false, usageError("unsupported profile delete argument %q", arg)
			}
			if id != "" {
				return "", false, usageError("profile delete accepts exactly one profile id")
			}
			id = arg
		}
	}
	if id == "" {
		return "", false, usageError("profile delete requires a profile id")
	}
	return id, yes, nil
}

func parseOptionalJSON(args []string, command string) (bool, error) {
	var jsonOutput bool
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
			continue
		}
		return false, usageError("unsupported %s argument %q", command, arg)
	}
	return jsonOutput, nil
}

func cutFlagValue(arg string) (string, bool) {
	_, value, ok := strings.Cut(arg, "=")
	return value, ok
}

func flagValue(flag string, args []string, index int, inlineValue string, hasInlineValue bool) (string, int, error) {
	if hasInlineValue {
		if strings.TrimSpace(inlineValue) == "" {
			return "", index, usageError("%s requires a value", flag)
		}
		return inlineValue, index, nil
	}
	if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" || strings.HasPrefix(args[index+1], "--") {
		return "", index, usageError("%s requires a value", flag)
	}
	return args[index+1], index + 1, nil
}

func writeJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func okJSON(fields map[string]any) map[string]any {
	response := map[string]any{
		"schema_version": "v1",
		"status":         "ok",
		"warnings":       []string{},
		"errors":         []string{},
	}
	for key, value := range fields {
		response[key] = value
	}
	return response
}

func profileCommandError(err error) error {
	switch {
	case errors.Is(err, profile.ErrNotFound):
		return exitError{code: 1, err: err}
	case errors.Is(err, profile.ErrAlreadyExists):
		return exitError{code: 1, err: err}
	case profile.IsValidationError(err):
		return usageError("%s", err.Error())
	default:
		return err
	}
}

func profilesForOutput(profiles []profile.Profile) []profile.Profile {
	out := make([]profile.Profile, len(profiles))
	for i, p := range profiles {
		out[i] = profileForOutput(p)
	}
	return out
}

func profileForOutput(p profile.Profile) profile.Profile {
	p.ID = render.Redact(p.ID)
	p.Name = render.Redact(p.Name)
	p.Server = render.Redact(p.Server)
	p.Protocol = render.Redact(p.Protocol)
	p.UserIdentity = redactedProfileUserIdentity(p)
	p.Transport = render.Redact(p.Transport)
	p.Security = render.Redact(p.Security)
	p.Encryption = render.Redact(p.Encryption)
	p.Flow = render.Redact(p.Flow)
	p.ServerName = render.Redact(p.ServerName)
	p.ALPN = render.Redact(p.ALPN)
	p.Fingerprint = render.Redact(p.Fingerprint)
	p.Path = render.Redact(p.Path)
	p.HostHeader = render.Redact(p.HostHeader)
	p.ServiceName = render.Redact(p.ServiceName)
	p.RealityPublicKey = render.Redact(p.RealityPublicKey)
	p.RealityShortID = render.Redact(p.RealityShortID)
	p.RealitySpiderX = render.Redact(p.RealitySpiderX)
	return p
}

func validateProfileForMode(p profile.Profile, mode string) error {
	if err := profile.Validate(p); err != nil {
		return err
	}
	switch mode {
	case planner.ModeProxyOnly:
		return engine.ValidateXrayProxyOnlyProfile(p)
	case planner.ModeTun:
		return engine.ValidateXrayTunProfile(p)
	default:
		return fmt.Errorf("unsupported profile validation mode %q", mode)
	}
}

func redactedProfileUserIdentity(p profile.Profile) string {
	if strings.TrimSpace(p.UserIdentity) == "" {
		return ""
	}
	switch strings.ToLower(p.Protocol) {
	case "trojan", "shadowsocks":
		return "REDACTED"
	default:
		return render.Redact(p.UserIdentity)
	}
}

func printOptionalProfileField(w io.Writer, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(w, "%s: %s\n", label, value)
}

func printProfileHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden profile add --name <name> --server <host> --port <port> --protocol <vless|vmess|trojan|shadowsocks>
  tunwarden profile import <share-uri>
  tunwarden profile list [--json]
  tunwarden profile show <profile-id> [--json]
  tunwarden profile validate <profile-id> [--mode proxy-only|tun] [--json]
  tunwarden profile delete <profile-id> --yes

Manage profiles in local TunWarden user state. These commands never start
network processes and never mutate TUN, routes, DNS, nftables, or firewall state.

Implemented in v0.1:
  manual profile add/list/show/validate/delete, VLESS/VMess/Trojan/Shadowsocks
  share URI import, validation, JSON list/show/validate output, and atomic local
  profile storage under the documented XDG user state location.

Not implemented yet:
  profile import --json, profile inspect/explain, Xray config generation for
  non-VLESS imported profiles, connect/disconnect behavior
`)
}
