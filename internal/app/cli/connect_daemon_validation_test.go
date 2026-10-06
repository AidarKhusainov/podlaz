package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestRunCLIConnectRejectsTunIncompatibleProfileBeforeDaemon(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(profile.Profile) profile.Profile
		wantMessage string
	}{
		{
			name: "unsupported engine",
			mutate: func(p profile.Profile) profile.Profile {
				p.ID = "tun-amneziawg-profile"
				p.Engine = profile.EngineAmneziaWG
				return p
			},
			wantMessage: "TUN-mode Xray config requires engine",
		},
		{
			name: "xhttp remains proxy only",
			mutate: func(p profile.Profile) profile.Profile {
				p.ID = "tun-xhttp-profile"
				p.Transport = "xhttp"
				p.Path = "/xhttp"
				return p
			},
			wantMessage: "Proxy only",
		},
		{
			name: "unsupported security",
			mutate: func(p profile.Profile) profile.Profile {
				p.ID = "tun-xtls-profile"
				p.Security = "xtls"
				return p
			},
			wantMessage: "unsupported TUN-mode VLESS security",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storePath := t.TempDir() + "/profiles.json"
			p := tt.mutate(testConnectProfile())
			store, err := profile.NewStore(storePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Add(p); err != nil {
				t.Fatal(err)
			}

			calledDaemon := false
			err = runWithOptions(context.Background(), []string{"connect", p.ID}, &bytes.Buffer{}, options{
				profileStorePath: storePath,
				connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
					calledDaemon = true
					return api.LifecycleResponse{}, nil
				},
			})
			if err == nil {
				t.Fatal("expected unsupported profile to fail")
			}
			if calledDaemon {
				t.Fatal("unsupported profile reached daemon")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected %q in %v", tt.wantMessage, err)
			}
		})
	}
}

func TestRunDebugProxyRejectsUnsupportedProxyProfileBeforeDaemon(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	p.ID = "quic-profile"
	p.Transport = "quic"
	store, _ := profile.NewStore(storePath)
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}

	calledDaemon := false
	err := runWithOptions(context.Background(), []string{"debug", "proxy", p.ID}, &bytes.Buffer{}, options{
		profileStorePath: storePath,
		connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
			calledDaemon = true
			return api.LifecycleResponse{}, nil
		},
	})
	if err == nil || calledDaemon {
		t.Fatalf("debug proxy err=%v calledDaemon=%v", err, calledDaemon)
	}
}
