package recovery

import (
	"context"
	"testing"
)

func TestObserveResolvedLinkFailsClosedForMalformedTargetSection(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
	}{
		{
			name: "missing separator in DNS servers field",
			stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
       DNS Servers 203.0.113.53`,
		},
		{
			name: "empty DNS servers field",
			stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
       DNS Servers:`,
		},
		{
			name: "truncated domain field",
			stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
        DNS Domain`,
		},
		{
			name: "DNS scope without concrete configuration",
			stdout: `Link 7 (podlaz0)
    Current Scopes: DNS
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := observeResolvedLink(context.Background(), CommandResult{Stdout: tt.stdout}, nil)
			if got != resolvedLinkUnknown {
				t.Fatalf("malformed or partial target section must fail closed as unknown, got %v", got)
			}
		})
	}
}
