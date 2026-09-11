package recovery

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestAllRolledBackTransactionAbsenceVerifiesEvidenceWithoutCurrentTransaction(t *testing.T) {
	for _, tc := range []struct {
		name               string
		orphanRoutePresent bool
		wantErr            string
	}{
		{name: "clean evidence", orphanRoutePresent: false},
		{name: "remaining route", orphanRoutePresent: true, wantErr: "exact route remains present: 128.0.0.0/1 table 51820"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			saveOrphanRolledBackRouteEvidence(t, runtimeDir)
			runner := orphanRolledBackEvidenceRunner{
				terminalAbsenceRunner: terminalAbsenceRunner{},
				orphanRoutePresent:    tc.orphanRoutePresent,
			}

			err := verifyAllRolledBackTransactionAbsenceWithOptions(
				context.Background(),
				runtimeDir,
				rolledBackTransactionAbsenceOptions{
					Runner:     runner,
					PathExists: func(string) (bool, error) { return false, nil },
					ReadFile:   func(string) ([]byte, error) { return nil, os.ErrNotExist },
				},
			)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("clean rolled-back evidence blocked absence proof: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("remaining evidence error=%v, want containing %q", err, tc.wantErr)
			}
		})
	}
}
