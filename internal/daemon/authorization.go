package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const (
	PolkitAuthorizationEnv = "PODLAZ_POLKIT_AUTHORIZATION"
	polkitActionPrefix     = "io.github.aidarkhusainov.podlaz."
)

const (
	ActionConnectProxyOnly AuthorizationAction = polkitActionPrefix + "connect-proxy-only"
	ActionConnectTun       AuthorizationAction = polkitActionPrefix + "connect-tun"
	ActionDisconnect       AuthorizationAction = polkitActionPrefix + "disconnect"
	ActionRecoverExecute   AuthorizationAction = polkitActionPrefix + "recover-execute"
)

var (
	ErrAuthorizationDenied      = errors.New("authorization denied")
	ErrAuthorizationUnavailable = errors.New("authorization unavailable")
)

type AuthorizationAction string

type PeerSubject struct {
	PID       int
	UID       uint32
	GID       uint32
	StartTime uint64
}

type Authorizer interface {
	Authorize(context.Context, AuthorizationAction, PeerSubject) error
}

type peerCredentialRequirement interface {
	RequiresPeerCredentials() bool
}

type AllowAuthorizer struct{}

func (AllowAuthorizer) Authorize(context.Context, AuthorizationAction, PeerSubject) error {
	return nil
}

func (AllowAuthorizer) RequiresPeerCredentials() bool { return false }

type StaticErrorAuthorizer struct{ Err error }

func (a StaticErrorAuthorizer) Authorize(context.Context, AuthorizationAction, PeerSubject) error {
	if a.Err == nil {
		return nil
	}
	return a.Err
}

func (StaticErrorAuthorizer) RequiresPeerCredentials() bool { return false }

func authorizerFromEnv() Authorizer {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(PolkitAuthorizationEnv)))
	switch mode {
	case "", "0", "false", "no", "off", "disabled":
		return AllowAuthorizer{}
	case "1", "true", "yes", "on", "required", "polkit":
		return PolkitAuthorizer{AllowUserInteraction: true}
	default:
		return StaticErrorAuthorizer{Err: fmt.Errorf("%w: invalid %s value %q; use disabled or required", ErrAuthorizationUnavailable, PolkitAuthorizationEnv, mode)}
	}
}

type peerSubjectContextKey struct{}

func contextWithPeerSubject(ctx context.Context, subject PeerSubject) context.Context {
	return context.WithValue(ctx, peerSubjectContextKey{}, subject)
}

func peerSubjectFromContext(ctx context.Context) (PeerSubject, bool) {
	subject, ok := ctx.Value(peerSubjectContextKey{}).(PeerSubject)
	if !ok || subject.PID <= 0 {
		return PeerSubject{}, false
	}
	return subject, true
}

func authorizeHTTPRequest(r *http.Request, authorizer Authorizer, action AuthorizationAction) error {
	if authorizer == nil {
		authorizer = AllowAuthorizer{}
	}
	var subject PeerSubject
	if requiresPeerCredentials(authorizer) {
		var ok bool
		subject, ok = peerSubjectFromContext(r.Context())
		if !ok {
			return fmt.Errorf("%w: daemon could not identify local peer process for %s", ErrAuthorizationUnavailable, action)
		}
	}
	return authorizer.Authorize(r.Context(), action, subject)
}

func requiresPeerCredentials(authorizer Authorizer) bool {
	requirement, ok := authorizer.(peerCredentialRequirement)
	return !ok || requirement.RequiresPeerCredentials()
}

func writeAuthorizationHTTPError(w http.ResponseWriter, err error) {
	status := http.StatusForbidden
	if errors.Is(err, ErrAuthorizationUnavailable) {
		status = http.StatusServiceUnavailable
	}
	http.Error(w, err.Error(), status)
}
