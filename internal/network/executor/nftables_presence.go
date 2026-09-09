package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// observeNftTablePresence is read-only identity evidence. It proves absence
// only from a successful structured enumeration of the current tables; command
// failures, malformed documents, unsupported schema, and ambiguous duplicate
// identities remain unknown/errors.
func observeNftTablePresence(ctx context.Context, runner CommandRunner, family, table string) (bool, error) {
	if strings.TrimSpace(family) == "" || strings.TrimSpace(table) == "" {
		return false, errors.New("nftables table presence requires exact family and name")
	}
	result, err := observeCommand(ctx, runner, "nft", "-j", "list", "tables")
	if err != nil {
		return false, fmt.Errorf("enumerate nftables tables: %w", err)
	}
	present, err := parseNftTablePresenceJSON(result.Stdout, family, table)
	if err != nil {
		return false, fmt.Errorf("decode nftables table enumeration: %w", err)
	}
	return present, nil
}

func parseNftTablePresenceJSON(output, family, table string) (bool, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var doc nftJSONDocument
	if err := decoder.Decode(&doc); err != nil {
		return false, fmt.Errorf("invalid nftables JSON: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return false, fmt.Errorf("invalid nftables JSON: %w", err)
	}
	if len(doc.Nftables) == 0 {
		return false, errors.New("nftables JSON is empty")
	}

	seenMeta := false
	matches := 0
	for objectIndex, object := range doc.Nftables {
		if len(object) != 1 {
			return false, fmt.Errorf("nftables JSON object[%d] has %d members, want 1", objectIndex, len(object))
		}
		for kind, raw := range object {
			switch kind {
			case "metainfo":
				if seenMeta || objectIndex != 0 {
					return false, errors.New("nftables JSON metainfo is duplicated or out of order")
				}
				var meta nftJSONMetaInfo
				if err := decodeStrictNftJSON(raw, &meta); err != nil {
					return false, fmt.Errorf("invalid nftables JSON metainfo: %w", err)
				}
				if meta.JSONSchemaVersion != supportedNftJSONSchemaVersion {
					return false, fmt.Errorf("unsupported nftables JSON schema version %d", meta.JSONSchemaVersion)
				}
				seenMeta = true
			case "table":
				if !seenMeta {
					return false, errors.New("nftables table appears before metainfo")
				}
				// Occupancy needs only exact table identity. Additional documented
				// table metadata is irrelevant to this read-only question and is
				// intentionally not interpreted as ownership or composition proof.
				var identity struct {
					Family string `json:"family"`
					Name   string `json:"name"`
				}
				if err := json.Unmarshal(raw, &identity); err != nil {
					return false, fmt.Errorf("invalid nftables table identity: %w", err)
				}
				if strings.TrimSpace(identity.Family) == "" || strings.TrimSpace(identity.Name) == "" {
					return false, errors.New("nftables table enumeration contains incomplete identity")
				}
				if identity.Family == family && identity.Name == table {
					matches++
				}
			default:
				return false, fmt.Errorf("unexpected nftables JSON object %q in table enumeration", kind)
			}
		}
	}
	if !seenMeta {
		return false, errors.New("nftables JSON metainfo is missing")
	}
	if matches > 1 {
		return false, fmt.Errorf("duplicate nftables table identity %s %s", family, table)
	}
	return matches == 1, nil
}
