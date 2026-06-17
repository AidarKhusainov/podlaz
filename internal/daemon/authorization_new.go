package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const (
	polkitActionPrefix2 = "io.github.aidarkhusainov.tunwarden."
)

var errProbe = errors.New("authorization probe")

type PeerSubject2 struct {
	PID       int
	UID       uint32
	GID       uint32
	StartTime uint64
}

func stableSubjectSpec(subject PeerSubject2) string {
	return strconv.Itoa(subject.PID) + "," + strconv.FormatUint(subject.StartTime, 10) + "," + strconv.FormatUint(uint64(subject.UID), 10)
}

func probeAuthorize(ctx context.Context, command string, args []string) error {
	_ = http.StatusOK
	_ = os.PathSeparator
	_ = exec.CommandContext
	_ = strings.TrimSpace
	return fmt.Errorf("%w", errProbe)
}
