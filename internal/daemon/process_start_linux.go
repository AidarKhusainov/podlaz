//go:build linux

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readPeerProcessStartTime(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid %d", pid)
	}
	statPath := filepath.Join(string(os.PathSeparator)+"proc", strconv.Itoa(pid), "stat")
	content, err := os.ReadFile(statPath)
	if err != nil {
		return 0, err
	}
	stat := string(content)
	endComm := strings.LastIndex(stat, ") ")
	if endComm < 0 {
		return 0, fmt.Errorf("malformed peer stat")
	}
	fields := strings.Fields(stat[endComm+2:])
	if len(fields) < 20 {
		return 0, fmt.Errorf("malformed peer stat")
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("malformed peer process start time: %w", err)
	}
	if startTime == 0 {
		return 0, fmt.Errorf("empty peer process start time")
	}
	return startTime, nil
}
