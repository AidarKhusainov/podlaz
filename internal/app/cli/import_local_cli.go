package cli

import (
	"fmt"
	"io"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

func runLocalFileImport(path string, stdout io.Writer, opts options) error {
	content, err := profile.ReadLocalImportFile(path)
	if err != nil {
		return err
	}
	result, err := sub.ParseLocalImportContent(content)
	if err != nil {
		return usageError("%s", render.Redact(err.Error()))
	}
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}
	if len(result.Profiles) == 1 {
		if _, err := store.AddAndSelectIfUnset(result.Profiles[0]); err != nil {
			return profileCommandError(err)
		}
	} else if err := store.AddProfiles(result.Profiles); err != nil {
		return profileCommandError(err)
	}
	return printLocalImportResult(stdout, store, result)
}

func printLocalImportResult(stdout io.Writer, store profile.Store, result profile.LocalImportResult) error {
	fmt.Fprintf(stdout, "Imported %d profile", len(result.Profiles))
	if len(result.Profiles) != 1 {
		fmt.Fprint(stdout, "s")
	}
	fmt.Fprintln(stdout)
	if len(result.Profiles) == 1 {
		fmt.Fprintf(stdout, "Profile: %s\n", render.Redact(result.Profiles[0].Name))
	}
	if len(result.Unsupported) > 0 {
		fmt.Fprintf(stdout, "Skipped unsupported entries: %d\n", len(result.Unsupported))
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stdout, "Warning: %s\n", render.Redact(warning.Message))
	}
	selectedID, err := store.SelectedID()
	if err != nil {
		return err
	}
	if selectedID == "" {
		fmt.Fprintln(stdout, "Next: podlaz profile use <profile>")
		return nil
	}
	fmt.Fprintln(stdout, "Next: podlaz connect")
	return nil
}
