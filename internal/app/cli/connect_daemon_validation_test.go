package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

func TestRunCLIConnectRejectsUnsupportedProfileBeforeDaemon(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		mutate      func(profile.Profile) profile.Profile
		wantMessage string
	}{
		{
			name: "proxy-only unsupported engine",
			mode: planner.ModeProxyOnly,
			mutate: func(p profile.Profile) profile.Profile {
				p.ID = "amneziawg-profile"
				p.Engine = profile.EngineAmneziaWG
				return p
			},
			wantMessage: "proxy-only Xray config requires engine",
		},
		{
			name: "proxy-only unsupported transport",
			mode: planner.ModeProxyOnly,
			mutate: func(p profile.Profile) profile.Profile {
				p.ID = "xhttp-profile"
				p.Transport = "xhttp"
				return p
			},
			wantMessage: "unsupported proxy-only VLESS transport",
		},
		{
			name: "proxy-only unsupported security",
			mode: planner.ModeProxyOnly,
			mutate: func(p profile.Profile) profile.Profile {
				p.ID = "xtls-profile"
				p.Security = "xtls"
				return p
			},
			wantMessage: "unsupported proxy-only VLESS security",
		},
		{
			name: "proxy-only reality without public key",
			mode: planner.ModeProxyOnly,
			mutate: func(p profile.Profile) profile.Profile {
				p.ID = "reality-missing-key-profile"
				p.Security = "reality"
				p.RealityPublicKey = ""
				return p
			},
			wantMessage: "requires reality_public_key",
		},
		{
			name: "tun unsupported transport",
			mode: planner.ModeTun,
			mutate: func(p profile.Profile) profile.Profile {
				p.ID = "tun-xhttp-profile"
				p.Transport = "xhttp"
				return p
			},
			wantMessage: "unsupported TUN-mode VLESS transport",
		},
		{
			name: "tun unsupported security",
			mode: planner.ModeTun,
			mutate: func(p profile.Profile) profile.Profile {
				p.ID = "tun-xtls-profile"
				p.Security = "xtls"
				return p
			},
			wantMessage: "unsupported TUN-mode VLESS security",
		},
		{
			name: "tun reality without public key",
			mode: planner.ModeTun,
			mutate: func(p profile.Profile) profile.Profile {
				p.ID = "tun-reality-missing-key-profile"
				p.Security = "reality"
				p.RealityPublicKey = ""
				return p
			},
			wantMessage: "requires reality_public_key",
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
			var out bytes.Buffer
			err = runWithOptions(context.Background(), []string{"connect", "--mode", tt.mode, p.ID}, &out, options{
				profileStorePath: storePath,
				connect: func(context.Context, profile.Profile, string) (api.LifecycleResponse, error) {
					calledDaemon = true
					return api.LifecycleResponse{}, nil
				},
			})
			if err == nil {
				t.Fatal("expected unsupported profile to fail")
			}
			if calledDaemon {
				t.Fatal("unsupported profile was sent to the daemon")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected error containing %q, got %v", tt.wantMessage, err)
			}
		})
	}
}
