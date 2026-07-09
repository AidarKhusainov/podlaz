package daemon

import "testing"

func TestIsXrayTunRuntimeStartupFailure(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "operation not permitted after server creation starts",
			text: "Failed to start: main: failed to create server > operation not permitted",
			want: true,
		},
		{
			name: "permission denied after server creation starts",
			text: "Failed to start: main: failed to create server > permission denied",
			want: true,
		},
		{
			name: "schema error is not accepted",
			text: "Failed to start: main: failed to load config files: invalid field tunSettings",
			want: false,
		},
		{
			name: "unrelated permission error is not accepted",
			text: "Failed to start: main: failed to load config files > permission denied",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isXrayTunRuntimeStartupFailure(tt.text); got != tt.want {
				t.Fatalf("isXrayTunRuntimeStartupFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}
