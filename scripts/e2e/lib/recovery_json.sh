#!/usr/bin/env bash

assert_clean_recovery_json_file() {
  local path="$1"
  python3 - "${path}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("status") != "ok":
    raise SystemExit("recover JSON status is not ok")
if payload.get("warnings"):
    raise SystemExit("top-level recover JSON warnings remain")
recovery = payload.get("recovery")
if not isinstance(recovery, dict):
    raise SystemExit("recovery payload is missing")
if recovery.get("candidates"):
    raise SystemExit("recovery candidates remain")
if recovery.get("warnings"):
    raise SystemExit("recovery inspection warnings remain")
PY
}
