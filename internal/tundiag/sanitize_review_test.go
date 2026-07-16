package tundiag

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSanitizeReportCoversAllNetworkStringFields(t *testing.T) {
	report := SanitizeReport(Report{Network: Network{
		ServerHostname: "vpn.example.test token=hostname-secret\n",
		ServerName:     "authorization=sni-secret " + strings.Repeat("x", maxDiagnosticText+512) + "\r\n",
		NftablesStatus: "ready password=nftables-secret\t",
	}})

	for name, value := range map[string]string{
		"server hostname": report.Network.ServerHostname,
		"server name":     report.Network.ServerName,
		"nftables status": report.Network.NftablesStatus,
	} {
		if len(value) > maxDiagnosticText {
			t.Fatalf("%s exceeds max diagnostic text: %d", name, len(value))
		}
		if strings.ContainsAny(value, "\r\n\t") {
			t.Fatalf("%s contains control whitespace after sanitization: %q", name, value)
		}
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hostname-secret", "sni-secret", "nftables-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("sanitized report leaked %q: %s", secret, encoded)
		}
	}
}

func TestSanitizeProbeResultBoundsStructuredRuleEvidence(t *testing.T) {
	policyRules := make([]string, maxEvidenceItems+5)
	nftablesRules := make([]string, maxEvidenceItems+7)
	for i := range policyRules {
		policyRules[i] = fmt.Sprintf("%d: from all lookup main", i)
	}
	for i := range nftablesRules {
		nftablesRules[i] = fmt.Sprintf("rule-%d accept", i)
	}
	policyRules[0] = "100: from all lookup main token=policy-secret\n"
	policyRules[1] = strings.Repeat("p", maxDiagnosticText+256)
	nftablesRules[0] = "table inet podlaz password=nft-rule-secret\t"
	nftablesRules[1] = strings.Repeat("n", maxDiagnosticText+256)

	result := SanitizeProbeResult(ProbeResult{Evidence: Evidence{
		PolicyRules:   policyRules,
		NftablesRules: nftablesRules,
	}})

	if got := len(result.Evidence.PolicyRules); got != maxEvidenceItems {
		t.Fatalf("policy rules were not bounded: got %d want %d", got, maxEvidenceItems)
	}
	if got := len(result.Evidence.NftablesRules); got != maxEvidenceItems {
		t.Fatalf("nftables rules were not bounded: got %d want %d", got, maxEvidenceItems)
	}
	for name, values := range map[string][]string{
		"policy rules":   result.Evidence.PolicyRules,
		"nftables rules": result.Evidence.NftablesRules,
	} {
		for i, value := range values {
			if len(value) > maxDiagnosticText {
				t.Fatalf("%s[%d] exceeds max diagnostic text: %d", name, i, len(value))
			}
			if strings.ContainsAny(value, "\r\n\t") {
				t.Fatalf("%s[%d] contains control whitespace after sanitization: %q", name, i, value)
			}
		}
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"policy-secret", "nft-rule-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("sanitized probe evidence leaked %q: %s", secret, encoded)
		}
	}
}
