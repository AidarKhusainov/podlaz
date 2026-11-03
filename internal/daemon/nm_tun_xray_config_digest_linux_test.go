//go:build linux

package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestNMConfigDigestAcceptsOnlyExactOfficialPreflightBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xray.json")
	config := []byte(`{"inbounds":[]}`)
	if err := os.WriteFile(path, config, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(config)
	digest := hex.EncodeToString(sum[:])
	if err := verifyNMXrayConfigContent(path, digest); err != nil {
		t.Fatalf("exact pinned config refused: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"inbounds":[{}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyNMXrayConfigContent(path, digest); err == nil {
		t.Fatal("modified provider JSON admitted after preflight")
	}
	for _, bad := range []string{"", "A" + digest[1:], "z" + digest[1:], "abcd"} {
		if err := verifyNMXrayConfigContent(path, bad); err == nil {
			t.Fatal("invalid digest admitted")
		}
	}
	if err := os.Symlink(path, filepath.Join(filepath.Dir(path), "alias.json")); err != nil {
		t.Fatal(err)
	}
	if err := verifyNMXrayConfigContent(filepath.Join(filepath.Dir(path), "alias.json"), digest); err == nil {
		t.Fatal("symlinked official Xray configuration admitted")
	}
}

func TestNMPreparedConfigProducesDigestForActualTestedBytes(t *testing.T) {
	p := &nmOwnedXrayPrepared{
		config: []byte("{}"), interfaceName: "podlaz0", mtu: 1500, mark: 101,
	}
	p.digest = nmXrayAuthorityDigest(p.config, p.interfaceName, p.mtu, p.mark)
	sum := sha256.Sum256(p.config)
	if p.configSHA256() != hex.EncodeToString(sum[:]) {
		t.Fatal("prepared official configuration has wrong content digest")
	}
	p.mark = 102
	if p.configSHA256() != "" {
		t.Fatal("tampered preflight authority can produce an accepted content digest")
	}
}
