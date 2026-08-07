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
		{
			name: "missing DefaultRoute polarity",
			stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported`,
		},
		{
			name: "conflicting DefaultRoute polarity",
			stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: +DefaultRoute -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported`,
		},
		{
			name: "unknown target field",
			stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
        DNS Mystery: fixture`,
		},
		{
			name: "duplicate DNS servers field",
			stdout: `Link 7 (podlaz0)
    Current Scopes: DNS
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
       DNS Servers: 192.0.2.53
       DNS Servers: 192.0.2.54
        DNS Domain: ~.`,
		},
		{
			name: "duplicate target header",
			stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
Link 8 (podlaz0)
    Current Scopes: none
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported`,
		},
		{
			name: "malformed target header",
			stdout: `Link x (podlaz0)
    Current Scopes: none
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
