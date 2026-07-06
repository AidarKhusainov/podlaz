package daemon

import (
	"errors"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"testing"
)

func TestProxyOnlyCoreExecutionIdentityUsesCurrentDaemonUserWhenNotRoot(t *testing.T) {
	withCoreIdentityTestHooks(t, 1000, nil, nil)

	identity, err := proxyOnlyCoreExecutionIdentity()
	if err != nil {
		t.Fatalf("select proxy-only core identity: %v", err)
	}
	if identity.DropCredentials {
		t.Fatalf("expected non-root daemon to keep current unprivileged identity, got %#v", identity)
	}
	if len(identity.AmbientCaps) != 0 {
		t.Fatalf("proxy-only non-root identity must not request ambient capabilities, got %#v", identity.AmbientCaps)
	}

	permissions := identity.runtimeConfigPermissions()
	if permissions.DirMode != 0o700 || permissions.FileMode != 0o600 || permissions.Chown {
		t.Fatalf("unexpected non-root runtime config permissions: %#v", permissions)
	}
}

func TestProxyOnlyCoreExecutionIdentityUsesDedicatedIdentityWhenRoot(t *testing.T) {
	withDedicatedCoreIdentity(t)

	identity, err := proxyOnlyCoreExecutionIdentity()
	if err != nil {
		t.Fatalf("select proxy-only core identity: %v", err)
	}
	assertDedicatedCoreIdentity(t, identity)
	if len(identity.AmbientCaps) != 0 {
		t.Fatalf("proxy-only Xray must not receive ambient capabilities, got %#v", identity.AmbientCaps)
	}
}

func TestTunCoreExecutionIdentityUsesDedicatedIdentityWithNetAdminWhenRoot(t *testing.T) {
	withDedicatedCoreIdentity(t)

	identity, err := tunCoreExecutionIdentity()
	if err != nil {
		t.Fatalf("select TUN core identity: %v", err)
	}
	assertDedicatedCoreIdentity(t, identity)
	if len(identity.AmbientCaps) != 1 || identity.AmbientCaps[0] != syscall.CAP_NET_ADMIN {
		t.Fatalf("native TUN Xray must receive only CAP_NET_ADMIN as ambient capability, got %#v", identity.AmbientCaps)
	}

	cmd := exec.Command("core-test")
	configureCoreCommandCredential(cmd, identity)
	if cmd.SysProcAttr == nil || len(cmd.SysProcAttr.AmbientCaps) != 1 || cmd.SysProcAttr.AmbientCaps[0] != syscall.CAP_NET_ADMIN {
		t.Fatalf("expected native TUN command to carry CAP_NET_ADMIN ambient capability, got %#v", cmd.SysProcAttr)
	}
}

func TestProxyOnlyCoreExecutionIdentityFailsWhenDedicatedUserMissing(t *testing.T) {
	withCoreIdentityTestHooks(t, 0,
		func(name string) (*user.User, error) { return nil, errors.New("not found") },
		func(name string) (*user.Group, error) { return &user.Group{Name: name, Gid: "996"}, nil },
	)

	_, err := proxyOnlyCoreExecutionIdentity()
	if err == nil {
		t.Fatal("expected missing dedicated user to fail")
	}
	message := err.Error()
	if !strings.Contains(message, proxyCoreExecutionUser) || !strings.Contains(message, "packaging/sysusers.d/podlaz.conf") {
		t.Fatalf("expected actionable dedicated user error, got %v", err)
	}
}

func TestProxyOnlyCoreExecutionIdentityRejectsRootOrMismatchedDedicatedIdentity(t *testing.T) {
	t.Run("root ids", func(t *testing.T) {
		withCoreIdentityTestHooks(t, 0,
			func(name string) (*user.User, error) { return &user.User{Username: name, Uid: "0", Gid: "996"}, nil },
			func(name string) (*user.Group, error) { return &user.Group{Name: name, Gid: "996"}, nil },
		)
		_, err := proxyOnlyCoreExecutionIdentity()
		if err == nil || !strings.Contains(err.Error(), "must not resolve") {
			t.Fatalf("expected root identity rejection, got %v", err)
		}
	})

	t.Run("mismatched primary group", func(t *testing.T) {
		withCoreIdentityTestHooks(t, 0,
			func(name string) (*user.User, error) { return &user.User{Username: name, Uid: "997", Gid: "995"}, nil },
			func(name string) (*user.Group, error) { return &user.Group{Name: name, Gid: "996"}, nil },
		)
		_, err := proxyOnlyCoreExecutionIdentity()
		if err == nil || !strings.Contains(err.Error(), "primary group") {
			t.Fatalf("expected primary group rejection, got %v", err)
		}
	})
}

func withDedicatedCoreIdentity(t *testing.T) {
	t.Helper()
	withCoreIdentityTestHooks(t, 0,
		func(name string) (*user.User, error) {
			if name != proxyCoreExecutionUser {
				t.Fatalf("unexpected user lookup %q", name)
			}
			return &user.User{Username: name, Uid: "997", Gid: "996"}, nil
		},
		func(name string) (*user.Group, error) {
			if name != proxyCoreExecutionGroup {
				t.Fatalf("unexpected group lookup %q", name)
			}
			return &user.Group{Name: name, Gid: "996"}, nil
		},
	)
}

func assertDedicatedCoreIdentity(t *testing.T, identity coreExecutionIdentity) {
	t.Helper()
	if !identity.DropCredentials || identity.Name != proxyCoreExecutionUser || identity.UID != 997 || identity.GID != 996 {
		t.Fatalf("unexpected dedicated identity: %#v", identity)
	}

	cmd := exec.Command("core-test")
	configureCoreCommandCredential(cmd, identity)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential == nil {
		t.Fatalf("expected command credential to be configured")
	}
	credential := cmd.SysProcAttr.Credential
	if credential.Uid != 997 || credential.Gid != 996 {
		t.Fatalf("unexpected command credential: %#v", credential)
	}
	if credential.NoSetGroups {
		t.Fatalf("expected supplementary groups to be set explicitly, got %#v", credential)
	}
	if len(credential.Groups) != 0 {
		t.Fatalf("expected empty supplementary groups, got %#v", credential.Groups)
	}

	permissions := identity.runtimeConfigPermissions()
	if permissions.DirMode != 0o750 || permissions.FileMode != 0o640 || !permissions.Chown || permissions.UID != 0 || permissions.GID != 996 {
		t.Fatalf("unexpected root runtime config permissions: %#v", permissions)
	}
}

func withCoreIdentityTestHooks(t *testing.T, euid int, lookupUser func(string) (*user.User, error), lookupGroup func(string) (*user.Group, error)) {
	t.Helper()
	oldEUID := currentEUID
	oldLookupUser := lookupUserName
	oldLookupGroup := lookupGroupName
	currentEUID = func() int { return euid }
	if lookupUser != nil {
		lookupUserName = lookupUser
	}
	if lookupGroup != nil {
		lookupGroupName = lookupGroup
	}
	t.Cleanup(func() {
		currentEUID = oldEUID
		lookupUserName = oldLookupUser
		lookupGroupName = oldLookupGroup
	})
}
