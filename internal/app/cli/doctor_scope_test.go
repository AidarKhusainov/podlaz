package cli

import (
	"bytes"
	"context"
	"testing"
)

func TestRunCLIDoctorRejectsDeferredScopes(t *testing.T) {
	for _, scope := range []string{"--network", "--dns", "--routes", "--firewall"} {
		t.Run(scope, func(t *testing.T) {
			var out bytes.Buffer
			err := run(context.Background(), []string{"doctor", scope}, &out)
			assertUsageError(t, err, out.String(), "doctor "+scope+" is not implemented yet")
		})
	}
}
