#!/usr/bin/env python3
"""Fail-closed semantic predicates for the Podlaz daemon status API."""

import json
import re
import sys
from pathlib import Path


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


def bounded_token(value: object, default: str = "none") -> str:
    token = re.sub(r"[^a-z0-9_.-]+", "-", str(value or "").strip().lower()).strip("-.")
    return token or default


def load_optional_object(path: str | None) -> dict | None:
    if not path:
        return None
    candidate = Path(path)
    if not candidate.is_file() or candidate.is_symlink():
        return None
    with candidate.open(encoding="utf-8") as handle:
        payload = json.load(handle)
    if not isinstance(payload, dict):
        raise ValueError("daemon diagnostic payload must be a JSON object")
    return payload


def diagnose_active(status: dict, diagnostic: dict | None) -> str:
    if diagnostic is not None:
        stage = bounded_token(diagnostic.get("resume_stage"))
        outcome = bounded_token(diagnostic.get("last_resume_outcome"))
        if stage != "none" or outcome != "none":
            return ".".join(
                (
                    "resume",
                    stage,
                    outcome,
                    bounded_token(diagnostic.get("tun_failure_phase")),
                    bounded_token(diagnostic.get("network_apply_subphase")),
                    bounded_token(diagnostic.get("rollback_status")),
                    bounded_token(diagnostic.get("replay_disposition")),
                )
            )

    txs = transactions(status)
    if has_cleanup_required(txs):
        return "cleanup-required"

    connection = str(status.get("connection") or "")
    mode = str(status.get("mode") or "")
    health = status.get("tun_health") or {}
    if not isinstance(health, dict):
        return "invalid-tun-health"
    health_state = str(health.get("state") or "")
    terminal_reason = str(status.get("terminal_reason") or "")
    committed = [
        tx
        for tx in txs
        if tx.get("state") == "committed" and not bool(tx.get("requires_cleanup"))
    ]
    active_id = str(status.get("active_transaction_id") or "")

    if terminal_reason:
        return "terminal." + bounded_token(terminal_reason)
    if connection == "active" and mode == "tun" and health_state == "verified":
        if len(committed) != 1:
            return "active-verified-committed-count-invalid"
        if not active_id:
            return "active-missing-transaction-id"
        if active_id != str(committed[0].get("id") or ""):
            return "active-transaction-mismatch"
        return "verified-active"
    if connection == "active" and mode == "tun" and health_state:
        return "active." + bounded_token(health_state)
    if connection == "error (core exited)" and health_state:
        return "core-exited." + bounded_token(health_state)
    if connection == "inactive":
        return "inactive"
    return "status." + bounded_token(connection) + "." + bounded_token(health_state)



def diagnose_rebuild(status: dict, diagnostic: dict | None) -> str:
    health = status.get("tun_health") or {}
    if not isinstance(health, dict):
        health = {}
    session = {}
    if diagnostic is not None:
        raw_session = diagnostic.get("session") or {}
        if isinstance(raw_session, dict):
            session = raw_session

    core_running = session.get("core_running")
    if core_running is True:
        core_state = "core-running"
    elif core_running is False:
        core_state = "core-stopped"
    else:
        core_state = "core-unknown"

    return ".".join(
        (
            "rebuild",
            bounded_token(status.get("connection")),
            bounded_token(health.get("state")),
            bounded_token(health.get("classification")),
            bounded_token((diagnostic or {}).get("failure_phase")),
            bounded_token((diagnostic or {}).get("rollback_status")),
            bounded_token((diagnostic or {}).get("primary_classification")),
            bounded_token(session.get("state")),
            core_state,
        )
    )

def main() -> int:
    if len(sys.argv) < 3:
        return 2
    target, path = sys.argv[1], sys.argv[2]
    try:
        status = load_status(path)
        if target == "diagnose-active":
            if len(sys.argv) not in (3, 4):
                return 2
            diagnostic = load_optional_object(sys.argv[3] if len(sys.argv) == 4 else None)
            print(diagnose_active(status, diagnostic))
            return 0
        if target == "diagnose-rebuild":
            if len(sys.argv) not in (3, 4):
                return 2
            diagnostic = load_optional_object(sys.argv[3] if len(sys.argv) == 4 else None)
            print(diagnose_rebuild(status, diagnostic))
            return 0
        if len(sys.argv) != 3:
            return 2
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
