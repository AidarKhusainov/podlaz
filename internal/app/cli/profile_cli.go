package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
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
	case "list":
		return runProfileList(store, args[1:], stdout)
	case "show":
		return runProfileShow(store, args[1:], stdout)
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
		return writeJSON(stdout, map[string]any{"schema_version": "v1", "profiles": profiles})
	}

	fmt.Fprintln(stdout, "ID        NAME   PROTOCOL  SERVER       PORT")
	for _, p := range profiles {
		fmt.Fprintf(stdout, "%-9s %-6s %-9s %-12s %d\n", p.ID, p.Name, p.Protocol, p.Server, p.Port)
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

	if jsonOutput {
		return writeJSON(stdout, map[string]any{"schema_version": "v1", "profile": p})
	}

	fmt.Fprintf(stdout, "ID: %s\n", p.ID)
	fmt.Fprintf(stdout, "Name: %s\n", p.Name)
	fmt.Fprintf(stdout, "Source: %s\n", p.Source)
	fmt.Fprintf(stdout, "Engine: %s\n", p.Engine)
	fmt.Fprintf(stdout, "Protocol: %s\n", p.Protocol)
	fmt.Fprintf(stdout, "Server: %s\n", p.Server)
	fmt.Fprintf(stdout, "Port: %d\n", p.Port)
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
		return parsed, usageError(err.Error())
	}
	return parsed, nil
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

func profileCommandError(err error) error {
	switch {
	case errors.Is(err, profile.ErrNotFound):
		return exitError{code: 1, err: err}
	case errors.Is(err, profile.ErrAlreadyExists):
		return exitError{code: 1, err: err}
	case profile.IsValidationError(err):
		return usageError(err.Error())
	default:
		return err
	}
}

func printProfileHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden profile add --name <name> --server <host> --port <port> --protocol <vless|vmess|trojan|shadowsocks>
  tunwarden profile list [--json]
  tunwarden profile show <profile-id> [--json]
  tunwarden profile delete <profile-id> --yes

Manage manual profiles in local TunWarden user state. These commands never start
network processes and never mutate TUN, routes, DNS, nftables, or firewall state.

Implemented in v0.1:
  manual profile add, list, show, delete, validation, JSON list/show output, and
  atomic local profile storage under the documented XDG user state location.

Not implemented yet:
  profile import, VLESS URI import, subscriptions, Xray config generation,
  connect/disconnect behavior
`)
}
