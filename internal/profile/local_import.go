package profile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const MaxLocalImportSize = 4 * 1024 * 1024

type LocalImportFormat string

const (
	LocalImportFormatXrayJSON      LocalImportFormat = "xray-json"
	LocalImportFormatURIList       LocalImportFormat = "uri-list"
	LocalImportFormatBase64URIList LocalImportFormat = "base64-uri-list"
)

type LocalImportIssue struct {
	Entry   int    `json:"entry"`
	Message string `json:"message"`
}

type LocalImportResult struct {
	Format      LocalImportFormat  `json:"format"`
	Inspected   int                `json:"inspected"`
	Profiles    []Profile          `json:"profiles"`
	Unsupported []LocalImportIssue `json:"unsupported,omitempty"`
	Warnings    []LocalImportIssue `json:"warnings,omitempty"`
}

func ReadLocalImportFile(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("local import path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read local import file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxLocalImportSize+1))
	if err != nil {
		return nil, fmt.Errorf("read local import file: %w", err)
	}
	if len(data) > MaxLocalImportSize {
		return nil, fmt.Errorf("local import file exceeds 4 MiB limit")
	}
	return data, nil
}

func ImportLocalContent(content []byte) (LocalImportResult, error) {
	if len(content) > MaxLocalImportSize {
		return LocalImportResult{}, fmt.Errorf("local import file exceeds 4 MiB limit")
	}
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return LocalImportResult{}, fmt.Errorf("local import file is empty")
	}
	if trimmed[0] == '{' {
		return importXrayJSON(trimmed)
	}
	if json.Valid(trimmed) {
		var value any
		if err := json.Unmarshal(trimmed, &value); err == nil {
			return LocalImportResult{}, fmt.Errorf("unsupported local import JSON top-level type %s; expected Xray JSON object", jsonTopLevelType(value))
		}
	}

	plain, plainErr := importURIList(content, LocalImportFormatURIList)
	if plainErr != nil {
		return LocalImportResult{}, plainErr
	}
	if len(plain.Profiles) > 0 {
		return plain, nil
	}

	decoded, err := decodeLocalImportBase64(content)
	if err == nil {
		base64Result, base64Err := importURIList(decoded, LocalImportFormatBase64URIList)
		if base64Err != nil {
			return LocalImportResult{}, base64Err
		}
		if len(base64Result.Profiles) > 0 {
			return base64Result, nil
		}
		if len(base64Result.Unsupported) > 0 {
			return LocalImportResult{}, fmt.Errorf("local Base64 URI-list contains no supported profiles; first unsupported entry %d: %s", base64Result.Unsupported[0].Entry, base64Result.Unsupported[0].Message)
		}
	}
	if len(plain.Unsupported) > 0 {
		return LocalImportResult{}, fmt.Errorf("local URI-list contains no supported profiles; first unsupported entry %d: %s", plain.Unsupported[0].Entry, plain.Unsupported[0].Message)
	}
	return LocalImportResult{}, fmt.Errorf("local import file contains no supported profiles")
}

func importXrayJSON(content []byte) (LocalImportResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return LocalImportResult{}, fmt.Errorf("malformed Xray JSON config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return LocalImportResult{}, fmt.Errorf("malformed Xray JSON config: trailing data")
	}
	if object == nil {
		return LocalImportResult{}, fmt.Errorf("Xray JSON config must be an object")
	}

	rawOutbounds, ok := object["outbounds"]
	if !ok {
		return LocalImportResult{}, fmt.Errorf("unsupported Xray JSON config: outbounds array is required")
	}
	var outbounds []json.RawMessage
	if err := json.Unmarshal(rawOutbounds, &outbounds); err != nil {
		return LocalImportResult{}, fmt.Errorf("unsupported Xray JSON config: outbounds must be an array")
	}
	if len(outbounds) == 0 {
		return LocalImportResult{}, fmt.Errorf("Xray JSON contains no importable outbounds")
	}

	name := "Xray JSON profile"
	if raw, ok := object["remarks"]; ok {
		_ = json.Unmarshal(raw, &name)
	}
	if strings.TrimSpace(name) == "" || name == "Xray JSON profile" {
		for _, raw := range outbounds {
			var outbound map[string]json.RawMessage
			if json.Unmarshal(raw, &outbound) != nil {
				continue
			}
			var tag string
			if json.Unmarshal(outbound["tag"], &tag) == nil && strings.TrimSpace(tag) != "" {
				name = tag
				break
			}
		}
	}

	p, acceptedName, err := NewImportedFileProviderXrayConfig(name, content)
	if err != nil {
		return LocalImportResult{}, err
	}
	result := LocalImportResult{
		Format:    LocalImportFormatXrayJSON,
		Inspected: len(outbounds),
		Profiles:  []Profile{p},
	}
	if !acceptedName {
		result.Warnings = append(result.Warnings, LocalImportIssue{Entry: 1, Message: DisplayNameRejectedWarning})
	}
	return result, nil
}

func importURIList(content []byte, format LocalImportFormat) (LocalImportResult, error) {
	result := LocalImportResult{Format: format}
	seen := map[string]struct{}{}
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	for i, rawLine := range lines {
		entry := strings.TrimSpace(rawLine)
		if entry == "" {
			continue
		}
		lineNo := i + 1
		result.Inspected++
		p, warnings, err := ImportShareURI(entry)
		if err != nil {
			result.Unsupported = append(result.Unsupported, LocalImportIssue{Entry: lineNo, Message: err.Error()})
			continue
		}
		p.Source = SourceImportedFile
		if err := Validate(p); err != nil {
			result.Unsupported = append(result.Unsupported, LocalImportIssue{Entry: lineNo, Message: err.Error()})
			continue
		}
		if _, duplicate := seen[p.ID]; duplicate {
			return LocalImportResult{}, fmt.Errorf("duplicate profile id %q in local import", p.ID)
		}
		seen[p.ID] = struct{}{}
		result.Profiles = append(result.Profiles, p)
		for _, warning := range warnings {
			result.Warnings = append(result.Warnings, LocalImportIssue{Entry: lineNo, Message: warning})
		}
	}
	DeduplicateDisplayNames(result.Profiles)
	sort.SliceStable(result.Profiles, func(i, j int) bool { return result.Profiles[i].ID < result.Profiles[j].ID })
	return result, nil
}

func looksSecretLike(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	if uuidPattern.MatchString(v) {
		return true
	}
	for _, marker := range []string{"token", "password", "passwd", "secret", "priv" + "ate", "author" + "ization", "api" + "_key", "api" + "key"} {
		if strings.Contains(v, marker) {
			return true
		}
	}
	return false
}

func decodeLocalImportBase64(content []byte) ([]byte, error) {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, string(content))
	if compact == "" {
		return nil, fmt.Errorf("content is empty")
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := enc.DecodeString(compact)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("invalid Base64")
}

func jsonTopLevelType(value any) string {
	switch value.(type) {
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number, float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	case map[string]any:
		return "object"
	default:
		return "value"
	}
}
