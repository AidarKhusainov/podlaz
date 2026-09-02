#!/usr/bin/env bash

# Read-only host identity observations used exclusively as privacy/redaction
# needles. The output is sensitive by definition: callers must capture it and
# must never print it to logs or permanent evidence.
observe_host_sensitive_values() {
  {
    hostname -f 2>/dev/null || true
    ip -o -4 addr show scope global 2>/dev/null | awk '{split($4, value, "/"); print value[1]; print $2}'
    ip -o -6 addr show scope global 2>/dev/null | awk '{split($4, value, "/"); print value[1]; print $2}'
    ip -4 route show default 2>/dev/null | awk '{for (i=1; i<=NF; i++) {if ($i=="via" || $i=="dev") print $(i+1)}}'
  } | sed '/^[[:space:]]*$/d' | sort -u
}
