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
	proxyCoreExecutionUser  = "tunwarden-xray"
	proxyCoreExecutionGroup = "tunwarden-xray"
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
	if currentEUID() != 0 {
		return sameUserCoreExecutionIdentity(), nil
	}

	identity, err := dedicatedProxyCoreExecutionIdentity()
	if err != nil {
		return coreExecutionIdentity{}, err
	}
	return identity, nil
}

func dedicatedProxyCoreExecutionIdentity() (coreExecutionIdentity, error) {
	u, err := lookupUserName(proxyCoreExecutionUser)
	if err != nil {
		return coreExecutionIdentity{}, fmt.Errorf("resolve proxy-only Xray execution user %q: %w; install packaging/sysusers.d/tunwarden.conf or create the documented system user", proxyCoreExecutionUser, err)
	}
	g, err := lookupGroupName(proxyCoreExecutionGroup)
	if err != nil {
		return coreExecutionIdentity{}, fmt.Errorf("resolve proxy-only Xray execution group %q: %w; install packaging/sysusers.d/tunwarden.conf or create the documented system group", proxyCoreExecutionGroup, err)
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
		return coreExecutionIdentity{}, fmt.Errorf("proxy-only Xray execution identity %q must not resolve to uid=%d gid=%d", proxyCoreExecutionUser, uid, gid)
	}
	if userGID != gid {
		return coreExecutionIdentity{}, fmt.Errorf("proxy-only Xray execution user %q must use dedicated primary group %q", proxyCoreExecutionUser, proxyCoreExecutionGroup)
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
	if !identity.DropCredentials {
		return
	}
	attr := cmd.SysProcAttr
	if attr == nil {
		attr = &syscall.SysProcAttr{}
	}
	attr.Credential = &syscall.Credential{
		Uid:         uint32(identity.UID),
		Gid:         uint32(identity.GID),
		NoSetGroups: true,
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
