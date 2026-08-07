package recovery

import (
	"context"
	"testing"
	"time"
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
    Current Scopes: DNS
         Protocols: +DefaultRoute -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
       DNS Servers: 192.0.2.53
        DNS Domain: ~.`,
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

func TestObserveResolvedLinkFailsClosedForSuccessfulStatusWithStderr(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
	}{
		{
			name: "valid empty transient record",
			stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported`,
		},
		{
			name: "valid podlaz configuration",
			stdout: `Link 7 (podlaz0)
    Current Scopes: DNS
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
       DNS Servers: 192.0.2.53
        DNS Domain: ~.`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := observeResolvedLink(context.Background(), CommandResult{
				Stdout:    tt.stdout,
				Stderr:    "unexpected diagnostic",
				RawStderr: "unexpected diagnostic\n",
			}, nil)
			if got != resolvedLinkUnknown {
				t.Fatalf("successful status with unexpected stderr must fail closed as unknown, got %v", got)
			}
		})
	}
}

func TestObserveResolvedLinkRequiresLiveCallerContext(t *testing.T) {
	outputs := []struct {
		name   string
		stdout string
	}{
		{
			name: "valid empty transient record",
			stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported`,
		},
		{
			name: "valid podlaz configuration",
			stdout: `Link 7 (podlaz0)
    Current Scopes: DNS
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
       DNS Servers: 192.0.2.53
        DNS Domain: ~.`,
		},
	}
	contexts := []struct {
		name string
		new  func(t *testing.T) context.Context
	}{
		{
			name: "nil",
			new: func(t *testing.T) context.Context {
				return nil
			},
		},
		{
			name: "cancelled",
			new: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
		},
		{
			name: "deadline exceeded",
			new: func(t *testing.T) context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				<-ctx.Done()
				return ctx
			},
		},
	}

	for _, contextCase := range contexts {
		for _, outputCase := range outputs {
			t.Run(contextCase.name+"/"+outputCase.name, func(t *testing.T) {
				got := observeResolvedLink(contextCase.new(t), CommandResult{Stdout: outputCase.stdout}, nil)
				if got != resolvedLinkUnknown {
					t.Fatalf("missing or terminated context must make successful resolved status unknown, got %v", got)
				}
			})
		}
	}
}
