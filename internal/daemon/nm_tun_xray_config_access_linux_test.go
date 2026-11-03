//go:build linux

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNMRestrictedConfigAccessBeforeGenerationReservation(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(config, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	uid, gid := os.Getuid(), os.Getgid()
	if uid > 0 && gid > 0 {
		if err := verifyNMXrayConfigChildAccess(config, uid, gid); err != nil {
			t.Fatalf("accessible private config refused: %v", err)
		}
	}
	for _, tc := range []struct {
		name string
		path string
		uid  int
		gid  int
	}{
		{"missing identity", config, 0, gid},
		{"relative path", "xray.json", uid, gid},
		{"wrong identity", config, 2147483646, 2147483646},
		{"missing file", filepath.Join(dir, "missing.json"), uid, gid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := verifyNMXrayConfigChildAccess(tc.path, tc.uid, tc.gid); err == nil {
				t.Fatal("untrusted or inaccessible config admitted")
			}
		})
	}
}

func TestNMRestrictedConfigRejectsUntraversableParentAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "generated")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(child, "xray.json")
	if err := os.WriteFile(config, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	uid, gid := os.Getuid(), os.Getgid()
	// A different child identity cannot traverse the broker-owned 0700 dir
	// even if the config itself is world-readable.
	if err := os.Chmod(config, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyNMXrayConfigChildAccess(config, 2147483646, 2147483646); err == nil ||
		!strings.Contains(err.Error(), "directory") {
		t.Fatalf("untraversable parent accepted: %v", err)
	}
	if uid > 0 && gid > 0 {
		if err := verifyNMXrayConfigChildAccess(config, uid, gid); err != nil {
			t.Fatalf("ordinary directory access refused: %v", err)
		}
	}
	link := filepath.Join(dir, "alias")
	if err := os.Symlink(child, link); err != nil {
		t.Fatal(err)
	}
	if err := verifyNMXrayConfigChildAccess(filepath.Join(link, "xray.json"), uid, gid); err == nil {
		t.Fatal("symlinked parent directory admitted")
	}
	fileLink := filepath.Join(dir, "linked.json")
	if err := os.Symlink(config, fileLink); err != nil {
		t.Fatal(err)
	}
	if err := verifyNMXrayConfigChildAccess(fileLink, uid, gid); err == nil {
		t.Fatal("symlinked config admitted")
	}
}

func TestNMRestrictedConfigUsesDedicatedGroupWithoutSupplementaryGroups(t *testing.T) {
	if os.Getgid() == 0 {
		t.Skip("test requires a non-root primary group")
	}
	dir, err := os.MkdirTemp("", "nm-xray-group-access-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(path, []byte("{}"), 0o640); err != nil {
		t.Fatal(err)
	}
	childUID, childGID := os.Getuid()+1, os.Getgid()
	if err := verifyNMXrayConfigChildAccess(path, childUID, childGID); err != nil {
		t.Fatalf("group-readable runtime config refused: %v", err)
	}
	if err := verifyNMXrayConfigChildAccess(path, childUID, childGID+1); err == nil {
		t.Fatal("identity without owning group admitted")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyNMXrayConfigChildAccess(path, childUID, childGID); err == nil {
		t.Fatal("removed group traverse access was ignored")
	}
}
