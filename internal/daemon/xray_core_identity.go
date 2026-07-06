package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

const (
	proxyCoreExecutionUser  = "podlaz-xray"
	proxyCoreExecutionGroup = "podlaz-xray"

	coreExecutionIdentitySetupHint = "install the packaged service or create the documented system user from packaging/sysusers.d/podlaz.conf"
)

var (
	currentEUID     = os.Geteuid
	lookupUserName  = user.Lookup
	lookupGroupName = user.LookupGroup
)

type coreExecutionIdentity struct {
	Name            string
	UID             int
	GID             int
	DropCredentials bool
	AmbientCaps     []uintptr
}

type runtimeConfigPermissions struct {
	DirMode  os.FileMode
	FileMode os.FileMode
	UID      int
	GID      int
	Chown    bool
}

func sameUserCoreExecutionIdentity() coreExecutionIdentity {
	return coreExecutionIdentity{Name: "current-daemon-user"}
}

func proxyOnlyCoreExecutionIdentity() (coreExecutionIdentity, error) {
	return coreChildExecutionIdentity()
}

func tunCoreExecutionIdentity() (coreExecutionIdentity, error) {
	identity, err := coreChildExecutionIdentity()
	if err != nil {
		return coreExecutionIdentity{}, err
	}
	if identity.DropCredentials {
		identity.AmbientCaps = append(identity.AmbientCaps, syscall.CAP_NET_ADMIN)
	}
	return identity, nil
}

func coreChildExecutionIdentity() (coreExecutionIdentity, error) {
	if currentEUID() != 0 {
		return sameUserCoreExecutionIdentity(), nil
	}
	return dedicatedProxyCoreExecutionIdentity()
}

func dedicatedProxyCoreExecutionIdentity() (coreExecutionIdentity, error) {
	u, err := lookupUserName(proxyCoreExecutionUser)
	if err != nil {
		return coreExecutionIdentity{}, fmt.Errorf("resolve connection helper execution user %q: %w; %s", proxyCoreExecutionUser, err, coreExecutionIdentitySetupHint)
	}
	g, err := lookupGroupName(proxyCoreExecutionGroup)
	if err != nil {
		return coreExecutionIdentity{}, fmt.Errorf("resolve connection helper execution group %q: %w; %s", proxyCoreExecutionGroup, err, coreExecutionIdentitySetupHint)
	}

	uid, err := parseSystemID("user", proxyCoreExecutionUser, u.Uid)
	if err != nil {
		return coreExecutionIdentity{}, err
	}
	userGID, err := parseSystemID("primary group", proxyCoreExecutionUser, u.Gid)
	if err != nil {
		return coreExecutionIdentity{}, err
	}
	gid, err := parseSystemID("group", proxyCoreExecutionGroup, g.Gid)
	if err != nil {
		return coreExecutionIdentity{}, err
	}
	if uid == 0 || gid == 0 {
		return coreExecutionIdentity{}, fmt.Errorf("connection helper execution identity %q must not resolve to uid=%d gid=%d", proxyCoreExecutionUser, uid, gid)
	}
	if userGID != gid {
		return coreExecutionIdentity{}, fmt.Errorf("connection helper execution user %q must use dedicated primary group %q", proxyCoreExecutionUser, proxyCoreExecutionGroup)
	}

	return coreExecutionIdentity{
		Name:            proxyCoreExecutionUser,
		UID:             uid,
		GID:             gid,
		DropCredentials: true,
	}, nil
}

func parseSystemID(kind, name, value string) (int, error) {
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s id for %q: %w", kind, name, err)
	}
	return int(id), nil
}

func configureCoreCommandCredential(cmd *exec.Cmd, identity coreExecutionIdentity) {
	configureChildCommandCredential(cmd, identity)
}

func configureTunAdapterCommandCredential(cmd *exec.Cmd, identity coreExecutionIdentity) {
	configureChildCommandCredential(cmd, identity)
}

func configureChildCommandCredential(cmd *exec.Cmd, identity coreExecutionIdentity) {
	if !identity.DropCredentials && len(identity.AmbientCaps) == 0 {
		return
	}
	attr := cmd.SysProcAttr
	if attr == nil {
		attr = &syscall.SysProcAttr{}
	}
	if identity.DropCredentials {
		attr.Credential = &syscall.Credential{
			Uid:    uint32(identity.UID),
			Gid:    uint32(identity.GID),
			Groups: []uint32{},
		}
	}
	if len(identity.AmbientCaps) > 0 {
		attr.AmbientCaps = append(append([]uintptr(nil), attr.AmbientCaps...), identity.AmbientCaps...)
	}
	cmd.SysProcAttr = attr
}

func (identity coreExecutionIdentity) runtimeConfigPermissions() runtimeConfigPermissions {
	if identity.DropCredentials {
		return runtimeConfigPermissions{
			DirMode:  0o750,
			FileMode: 0o640,
			UID:      0,
			GID:      identity.GID,
			Chown:    true,
		}
	}
	return runtimeConfigPermissions{
		DirMode:  0o700,
		FileMode: 0o600,
	}
}
