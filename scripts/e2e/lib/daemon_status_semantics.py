#!/usr/bin/env python3
"""Fail-closed semantic predicates for the Podlaz daemon status API."""

import json
import sys


def load_status(path: str) -> dict:
    with open(path, encoding="utf-8") as handle:
        payload = json.load(handle)
    if not isinstance(payload, dict):
        raise ValueError("daemon status payload must be a JSON object")
    return payload


def transactions(status: dict) -> list[dict]:
    value = status.get("transactions") or []
    if not isinstance(value, list) or not all(isinstance(item, dict) for item in value):
        raise ValueError("daemon status transactions must be an array of objects")
    return value


def has_cleanup_required(txs: list[dict]) -> bool:
    return any(bool(tx.get("requires_cleanup")) for tx in txs)


def verified_active(status: dict) -> bool:
    txs = transactions(status)
    active_id = str(status.get("active_transaction_id") or "")
    health = status.get("tun_health") or {}
    if not isinstance(health, dict):
        return False
    committed = (
        active_id != ""
        and any(
            str(tx.get("id") or "") == active_id
            and tx.get("state") == "committed"
            and not bool(tx.get("requires_cleanup"))
            for tx in txs
        )
    )
    return (
        status.get("connection") == "active"
        and status.get("mode") == "tun"
        and health.get("state") == "verified"
        and committed
        and not has_cleanup_required(txs)
        and not status.get("terminal_reason")
    )


def clean_inactive(status: dict) -> bool:
    txs = transactions(status)
    active_id = str(status.get("active_transaction_id") or "")
    committed_count = sum(
        1
        for tx in txs
        if tx.get("state") == "committed" and not bool(tx.get("requires_cleanup"))
    )
    return (
        status.get("connection") == "inactive"
        and active_id == ""
        and committed_count == 0
        and not has_cleanup_required(txs)
        and not status.get("terminal_reason")
    )


def main() -> int:
    if len(sys.argv) != 3:
        return 2
    target, path = sys.argv[1], sys.argv[2]
    try:
        status = load_status(path)
        if target == "verified-active":
            matched = verified_active(status)
        elif target == "clean-inactive":
            matched = clean_inactive(status)
        else:
            return 2
    except (OSError, ValueError, json.JSONDecodeError):
        return 1
    return 0 if matched else 1


if __name__ == "__main__":
    raise SystemExit(main())
