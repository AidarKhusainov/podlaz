package logs

import (
	"reflect"
	"testing"
)

func TestBuildJournalctlArgsFlagMatrix(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "default daemon logs",
			want: []string{"--system", "--unit", DaemonUnit, "--no-pager", "--output", "short", "--lines", DefaultLines},
		},
		{
			name: "since replaces default line limit",
			opts: Options{Since: "1m"},
			want: []string{"--system", "--unit", DaemonUnit, "--no-pager", "--output", "short", "--since", "-1m"},
		},
		{
			name: "follow appends follow flag",
			opts: Options{Follow: true},
			want: []string{"--system", "--unit", DaemonUnit, "--no-pager", "--output", "short", "--lines", DefaultLines, "--follow"},
		},
		{
			name: "since and follow compose",
			opts: Options{Since: "1m", Follow: true},
			want: []string{"--system", "--unit", DaemonUnit, "--no-pager", "--output", "short", "--since", "-1m", "--follow"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustBuildJournalctlArgs(t, tt.opts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("buildJournalctlArgs(%#v) = %#v, want %#v", tt.opts, got, tt.want)
			}
		})
	}
}

func mustBuildJournalctlArgs(t *testing.T, opts Options) []string {
	t.Helper()
	args, err := buildJournalctlArgs(opts)
	if err != nil {
		t.Fatalf("buildJournalctlArgs(%#v): %v", opts, err)
	}
	return args
}
