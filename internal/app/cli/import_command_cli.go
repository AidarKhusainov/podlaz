package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func runImportCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printImportHelp(stdout)
		return nil
	}

	target, err := parseImportArgs(args)
	if err != nil {
		return err
	}

	u, err := url.Parse(target)
	if err != nil {
		return usageError("invalid import target: malformed URI or URL")
	}
	if u.Scheme == "" {
		return runLocalFileImport(target, stdout, opts)
	}

	switch strings.ToLower(u.Scheme) {
	case "vless", "vmess", "trojan", "ss":
		store, err := profile.NewStore(opts.profileStorePath)
		if err != nil {
			return err
		}
		return importShareProfile(store, target, stdout)
	case "file", "http", "https":
		return runSubscriptionImport(ctx, target, stdout, opts)
	default:
		return usageError("unsupported import scheme %q", u.Scheme)
	}
}

func parseImportArgs(args []string) (string, error) {
	var target string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return "", usageError("unsupported import argument %q", arg)
		}
		if target != "" {
			return "", usageError("import accepts exactly one URI, URL, or local path")
		}
		target = arg
	}
	if strings.TrimSpace(target) == "" {
		return "", usageError("import requires a URI, URL, or local path")
	}
	return target, nil
}

func printImportHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz import <uri|url|file>

Import supported VPN material through one entry point. Podlaz detects share URIs,
local files, and subscription sources, validates them, and persists them
atomically without connecting or changing privileged networking.
`)
}
