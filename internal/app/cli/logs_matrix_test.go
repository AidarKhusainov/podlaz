package cli

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/logs"
)

func TestRunCLILogsParsesAdditionalIssue160Flags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want logs.Options
	}{
		{name: "daemon", args: []string{"logs", "--daemon"}, want: logs.Options{}},
		{name: "core", args: []string{"logs", "--core"}, want: logs.Options{Core: true}},
		{name: "since-inline", args: []string{"logs", "--since=1m"}, want: logs.Options{Since: "1m"}},
		{name: "since-separate", args: []string{"logs", "--since", "1m"}, want: logs.Options{Since: "1m"}},
		{name: "follow-short", args: []string{"logs", "-f"}, want: logs.Options{Follow: true}},
		{name: "follow-long", args: []string{"logs", "--follow"}, want: logs.Options{Follow: true}},
		{name: "combined", args: []string{"logs", "--core", "--since=1m", "--follow"}, want: logs.Options{Core: true, Since: "1m", Follow: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got logs.Options
			err := runWithOptions(context.Background(), tt.args, &bytes.Buffer{}, options{
				logs: func(_ context.Context, _ io.Writer, opts logs.Options) error {
					got = opts
					return nil
				},
			})
			if err != nil {
				t.Fatalf("logs command failed: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected logs options %#v, got %#v", tt.want, got)
			}
		})
	}
}
