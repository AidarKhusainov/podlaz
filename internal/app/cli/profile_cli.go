package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
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
	case "list":
		return runProfileList(store, args[1:], stdout)
	case "show":
		return runProfileShow(store, args[1:], stdout)
	case "use":
		return runProfileUse(store, args[1:], stdout)
	case "delete":
		return runProfileDelete(store, args[1:], stdout, opts)
	default:
		return usageError("unknown profile subcommand %q", args[0])
	}
}

// importShareProfile is the share-URI branch of the single public import command.
func importShareProfile(store profile.Store, uri string, stdout io.Writer) error {
	p, warnings, err := profile.ImportShareURI(uri)
	if err != nil {
		return usageError("%s", err.Error())
	}
	if err := store.Add(p); err != nil {
		return profileCommandError(err)
	}
	_, _ = store.SelectIfUnset(p.ID)

	fmt.Fprintln(stdout, "Imported 1 profile")
	fmt.Fprintf(stdout, "Profile: %s\n", render.Redact(p.Name))
	for _, warning := range warnings {
		fmt.Fprintf(stdout, "Warning: %s\n", render.Redact(warning))
	}
	fmt.Fprintln(stdout, "Next: podlaz connect")
	return nil
}

func runProfileList(store profile.Store, args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return usageError("profile list does not accept arguments")
	}
	profiles, err := store.List()
	if err != nil {
		return err
	}
	selectedID, err := store.SelectedID()
	if err != nil {
		return err
	}

	rows := make([][]string, 0, len(profiles))
	for _, p := range profiles {
		selected := ""
		if p.ID == selectedID {
			selected = "*"
		}
		rows = append(rows, []string{selected, render.Redact(p.Name), render.Redact(p.Protocol), render.Redact(string(p.Source))})
	}
	return writeTable(stdout, []string{"SELECTED", "NAME", "PROTOCOL", "SOURCE"}, rows)
}

func runProfileShow(store profile.Store, args []string, stdout io.Writer) error {
	selector, err := parseSingleProfileSelector(args, "profile show")
	if err != nil {
		return err
	}
	p, err := store.Resolve(selector)
	if err != nil {
		return profileCommandError(err)
	}

	out := profileForOutput(p)
	fmt.Fprintf(stdout, "Name: %s\n", out.Name)
	fmt.Fprintf(stdout, "ID: %s\n", out.ID)
	fmt.Fprintf(stdout, "Source: %s\n", out.Source)
	fmt.Fprintf(stdout, "Engine: %s\n", out.Engine)
	fmt.Fprintf(stdout, "Protocol: %s\n", out.Protocol)
	fmt.Fprintf(stdout, "Server: %s\n", out.Server)
	fmt.Fprintf(stdout, "Port: %d\n", out.Port)
	printOptionalProfileField(stdout, "Transport", out.Transport)
	printOptionalProfileField(stdout, "Security", out.Security)
	printOptionalProfileField(stdout, "Flow", out.Flow)
	printOptionalProfileField(stdout, "Server name", out.ServerName)
	return nil
}

func runProfileUse(store profile.Store, args []string, stdout io.Writer) error {
	selector, err := parseSingleProfileSelector(args, "profile use")
	if err != nil {
		return err
	}
	p, err := store.Select(selector)
	if err != nil {
		return profileCommandError(err)
	}
	fmt.Fprintf(stdout, "Selected profile: %s\n", render.Redact(p.Name))
	return nil
}

func runProfileDelete(store profile.Store, args []string, stdout io.Writer, opts options) error {
	selector, yes, err := parseProfileDeleteArgs(args)
	if err != nil {
		return err
	}
	p, err := store.Resolve(selector)
	if err != nil {
		return profileCommandError(err)
	}
	if !yes {
		if !profileDeleteInputIsTerminal(opts) {
			return usageError("profile delete requires --yes in non-interactive mode")
		}
		prompt := fmt.Sprintf("Delete profile %s?", render.Redact(p.Name))
		if err := confirmDefaultNo(stdout, confirmationReader(opts), prompt, "profile delete", "profile delete canceled"); err != nil {
			return err
		}
	}
	if err := store.Delete(p.ID); err != nil {
		return profileCommandError(err)
	}
	fmt.Fprintf(stdout, "Profile deleted: %s\n", render.Redact(p.Name))
	return nil
}

func parseSingleProfileSelector(args []string, command string) (string, error) {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return "", usageError("%s requires exactly one profile", command)
	}
	return args[0], nil
}

func parseProfileDeleteArgs(args []string) (string, bool, error) {
	var selector string
	var yes bool
	for _, arg := range args {
		switch arg {
		case "--yes":
			yes = true
		default:
			if strings.HasPrefix(arg, "-") {
				return "", false, usageError("unsupported profile delete argument %q", arg)
			}
			if selector != "" {
				return "", false, usageError("profile delete accepts exactly one profile")
			}
			selector = arg
		}
	}
	if selector == "" {
		return "", false, usageError("profile delete requires a profile")
	}
	return selector, yes, nil
}

func profileDeleteInputIsTerminal(opts options) bool {
	if opts.stdinIsTerminal != nil {
		return opts.stdinIsTerminal()
	}
	return isStdinTerminal()
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
	case errors.Is(err, profile.ErrNotFound), errors.Is(err, profile.ErrNoSelection):
		return exitError{code: 1, err: err}
	case errors.Is(err, profile.ErrAmbiguousSelector):
		return exitError{code: 1, err: err}
	case errors.Is(err, profile.ErrAlreadyExists):
		return exitError{code: 1, err: err}
	case profile.IsValidationError(err):
		return usageError("%s", err.Error())
	default:
		return err
	}
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
	return render.Redact(p.UserIdentity)
}

func printOptionalProfileField(w io.Writer, label string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(w, "%s: %s\n", label, value)
}

func printProfileHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz profile list
  podlaz profile show <profile>
  podlaz profile use <profile>
  podlaz profile delete <profile> [--yes]

Profiles may be addressed by exact stable ID or an exact unique display name.
"profile use" changes only the selected profile for future user intent; it does
not connect, disconnect, or rewrite an existing autostart policy. The list view
marks the selected profile and intentionally omits endpoint and credential data.
`)
}
