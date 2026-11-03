//go:build linux

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNMConfinedXrayRejectsAdmissionBeforeGenerationMutation(t *testing.T) {
	dir := t.TempDir()
	s := nmConfinedXrayGeneration{
		l3:         newNML3Stager(dir, nil),
		generation: newNMGenerationFence(dir, nil),
	}
	t.Run("missing FD", func(t *testing.T) {
		err := s.Run(context.Background(), nil, nmTunFDKernelIdentity{},
			nmTunDBusAuthority{}, "", 0, "", "")
		if err == nil || !strings.Contains(err.Error(), "descriptor") {
			t.Fatalf("expected missing FD refusal, got %v", err)
		}
	})
	t.Run("missing context", func(t *testing.T) {
		fd, err := os.Open("/dev/null")
		if err != nil {
			t.Fatal(err)
		}
		defer fd.Close()
		if err := s.Run(nil, fd, nmTunFDKernelIdentity{},
			nmTunDBusAuthority{}, "", 0, "", ""); err == nil {
			t.Fatal("missing context admitted")
		}
	})
	if _, err := os.Stat(filepath.Join(dir, nmGenerationFile)); !os.IsNotExist(err) {
		t.Fatalf("refused admission unexpectedly reserved a generation: %v", err)
	}
}

func TestNMConfinedXrayRequiresRealDedicatedCgroup(t *testing.T) {
	tmp := t.TempDir()
	for _, tc := range []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"relative", "relative/cgroup"},
		{"ordinary directory", tmp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cg, err := openNMGenerationCgroup(tc.path)
			if cg != nil {
				cg.Close()
			}
			if err == nil {
				t.Fatal("unsafe cgroup accepted")
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(tmp, link); err != nil {
			t.Fatal(err)
		}
		cg, err := openNMGenerationCgroup(link)
		if cg != nil {
			cg.Close()
		}
		if err == nil {
			t.Fatal("symlink cgroup accepted")
		}
	})
}

func TestNMConfinedXrayRejectsFakeQuiescenceEvidence(t *testing.T) {
	file, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := verifyNMGenerationCgroupEmpty(file); err == nil {
		t.Fatal("directory without cgroup.events was classified quiescent")
	}
	if err := killNMGenerationCgroup(file); err == nil {
		t.Fatal("directory without cgroup.kill was classified revoked")
	}
}
