package tundiag

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	DiagnosticsDirName = "diagnostics"
	LastReportFileName = "tun-last.json"
	MaxReportBytes     = 256 * 1024
)

type Store struct {
	RuntimeDir string
	Now        func() time.Time
}

func (s Store) Path() string {
	runtimeDir := s.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = "/run/podlaz"
	}
	return filepath.Join(runtimeDir, DiagnosticsDirName, LastReportFileName)
}

func (s Store) Save(report Report) (string, error) {
	path := s.Path()
	report.SchemaVersion = SchemaVersion
	if report.GeneratedAt.IsZero() {
		report.GeneratedAt = s.now()
	}
	report.ReportPath = path
	report = Finalize(report)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode TUN diagnostic report: %w", err)
	}
	data = append(data, '\n')
	if len(data) > MaxReportBytes {
		return "", fmt.Errorf("TUN diagnostic report is %d bytes; limit is %d", len(data), MaxReportBytes)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create diagnostic directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tun-last-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create diagnostic temp file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("set diagnostic temp permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write diagnostic temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("sync diagnostic temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close diagnostic temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("replace diagnostic report %s: %w", path, err)
	}
	removeTemp = false
	if err := syncDirectory(dir); err != nil {
		return "", fmt.Errorf("sync diagnostic directory %s: %w", dir, err)
	}
	return path, nil
}

func (s Store) Load() (Report, string, error) {
	path := s.Path()
	file, err := os.Open(path)
	if err != nil {
		return Report{}, path, err
	}
	defer file.Close()
	limited := io.LimitReader(file, MaxReportBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Report{}, path, fmt.Errorf("read TUN diagnostic report: %w", err)
	}
	if len(data) > MaxReportBytes {
		return Report{}, path, fmt.Errorf("TUN diagnostic report exceeds %d bytes", MaxReportBytes)
	}
	var report Report
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return Report{}, path, fmt.Errorf("decode TUN diagnostic report: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Report{}, path, err
	}
	if report.SchemaVersion != SchemaVersion {
		return Report{}, path, fmt.Errorf("unsupported TUN diagnostic schema_version %d", report.SchemaVersion)
	}
	report.ReportPath = path
	report.Historical = true
	return Finalize(report), path, nil
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing TUN diagnostic data: %w", err)
	}
	return errors.New("TUN diagnostic report contains trailing JSON data")
}
