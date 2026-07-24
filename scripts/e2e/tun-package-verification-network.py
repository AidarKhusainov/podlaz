#!/usr/bin/env python3
"""Capture exact verification-only route and policy-rule obligations.

Durable applied/rollback ownership is always an absence obligation. During the
``applying`` crash window, desired tuples without durable proof are also always
obligations because current host presence cannot distinguish an in-flight
podlaz mutation from pre-existing state. In states outside that window, an
unowned desired tuple becomes an obligation only when the exact tuple is absent
at capture time, preserving validated pre-existing host state.

This manifest is safe only for later absence verification and must never
authorize mutation.
"""

from __future__ import annotations

import importlib.util
import json
import stat
import sys
from collections import Counter
from pathlib import Path

FALLBACK_PATH = Path(__file__).with_name("tun-package-fallback-network.py")
SPEC = importlib.util.spec_from_file_location("tun_package_fallback_network_shared", FALLBACK_PATH)
assert SPEC is not None and SPEC.loader is not None
NETWORK = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = NETWORK
SPEC.loader.exec_module(NETWORK)


def _desired_policy_rules(values: list[object]) -> list[object]:
    rules = []
    for value in values:
        step = NETWORK._dict(value, "desired step")
        if str(step.get("kind", "")).strip() != "policy-rule":
            continue
        target = str(step.get("target", "")).strip()
        owner = str(step.get("owner", "")).strip()
        if not target:
            raise NETWORK.MetadataError("desired policy-rule step lacks target")
        rules.append(NETWORK._policy_rule_from_target(target, owner))
    return rules


def _counter_difference(desired: list[object], durable: list[object]) -> list[object]:
    difference = Counter(desired) - Counter(durable)
    return [candidate for candidate, count in sorted(difference.items()) for _ in range(count)]


def _load_transaction(
    path: Path,
) -> tuple[
    list[object],
    list[object],
    list[object],
    list[object],
    list[object],
    list[object],
]:
    try:
        with path.open(encoding="utf-8") as handle:
            payload = json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        raise NETWORK.MetadataError("transaction file is unreadable or invalid JSON") from exc

    transaction = NETWORK._dict(payload, "transaction")
    if transaction.get("schema_version") != NETWORK.TRANSACTION_SCHEMA:
        raise NETWORK.MetadataError("transaction schema is unsupported")
    if transaction.get("owner") != NETWORK.TRANSACTION_OWNER:
        raise NETWORK.MetadataError("transaction owner is not podlaz")

    state = str(transaction.get("state", "")).strip()
    if state not in NETWORK.KNOWN_STATES:
        raise NETWORK.MetadataError("transaction state is unsupported")
    if state == "rolled_back":
        return [], [], [], [], [], []

    desired = NETWORK._dict(transaction.get("desired_plan", {}), "transaction desired plan")
    desired_route_values = NETWORK._list(desired.get("routes", []), "transaction desired routes")
    desired_step_values = NETWORK._list(desired.get("steps", []), "transaction desired steps")
    desired_routes_by_target = NETWORK._desired_routes(desired_route_values)
    desired_routes = [route for candidates in desired_routes_by_target.values() for route in candidates]
    desired_rules = _desired_policy_rules(desired_step_values)

    applied_steps = NETWORK._list(transaction.get("applied_steps", []), "transaction applied steps")
    rollback = NETWORK._dict(transaction.get("rollback", {}), "transaction rollback")
    rollback_route_values = NETWORK._list(
        rollback.get("routes", []), "transaction rollback routes"
    )
    rollback_rule_values = NETWORK._list(
        rollback.get("policy_rules", []), "transaction rollback policy rules"
    )
    durable_routes = [NETWORK.validated_route(value) for value in rollback_route_values]
    durable_rules = [NETWORK.validated_policy_rule(value) for value in rollback_rule_values]

    if state == "planned":
        if durable_routes or durable_rules or NETWORK._has_network_applied_step(applied_steps):
            raise NETWORK.MetadataError("planned transaction contains durable network ownership")
    elif state in NETWORK.CLEANUP_REQUIRED_STATES:
        NETWORK._require_exact_applied_coverage(
            desired_route_values,
            desired_step_values,
            applied_steps,
            durable_routes,
            durable_rules,
            require_desired_category_proof=False,
        )

    unowned_routes = _counter_difference(desired_routes, durable_routes)
    unowned_rules = _counter_difference(desired_rules, durable_rules)
    if state == "applying":
        return durable_routes, durable_rules, [], [], unowned_routes, unowned_rules
    return durable_routes, durable_rules, unowned_routes, unowned_rules, [], []


def _verification_obligations(
    durable: list[object],
    baseline_candidates: list[object],
    ambiguous_candidates: list[object],
    present,
) -> list[object]:
    obligations = list(durable)
    obligations.extend(ambiguous_candidates)
    for candidate in baseline_candidates:
        if not present(candidate):
            obligations.append(candidate)
    return obligations


def snapshot_verification_transactions(root: Path, manifest_path: Path):
    durable_routes = []
    durable_rules = []
    baseline_routes = []
    baseline_rules = []
    ambiguous_routes = []
    ambiguous_rules = []
    try:
        root_stat = root.stat()
    except FileNotFoundError:
        root_stat = None
    except OSError as exc:
        raise NETWORK.MetadataError("transaction directory cannot be inspected") from exc

    if root_stat is not None:
        if not stat.S_ISDIR(root_stat.st_mode):
            raise NETWORK.MetadataError("transaction path is not a directory")
        try:
            entries = sorted(root.iterdir())
        except OSError as exc:
            raise NETWORK.MetadataError("transaction directory cannot be read") from exc
        for entry in entries:
            try:
                entry_stat = entry.lstat()
            except OSError as exc:
                raise NETWORK.MetadataError(
                    "transaction directory entry cannot be inspected"
                ) from exc
            if not stat.S_ISREG(entry_stat.st_mode) or entry.suffix != ".json":
                raise NETWORK.MetadataError("transaction directory contains an unexpected entry")
        for path in entries:
            (
                tx_durable_routes,
                tx_durable_rules,
                tx_baseline_routes,
                tx_baseline_rules,
                tx_ambiguous_routes,
                tx_ambiguous_rules,
            ) = _load_transaction(path)
            durable_routes.extend(tx_durable_routes)
            durable_rules.extend(tx_durable_rules)
            baseline_routes.extend(tx_baseline_routes)
            baseline_rules.extend(tx_baseline_rules)
            ambiguous_routes.extend(tx_ambiguous_routes)
            ambiguous_rules.extend(tx_ambiguous_rules)

    inspection_cache: dict[tuple[str, ...], str] = {}

    def inspection_output(command: list[str]) -> str:
        key = tuple(command)
        if key not in inspection_cache:
            inspection_cache[key] = NETWORK._inspection_output(command)
        return inspection_cache[key]

    def route_present(route) -> bool:
        return NETWORK._route_present(route, inspection_output(route.show_command()))

    def rule_present(rule) -> bool:
        return NETWORK._rule_present(rule, inspection_output(rule.show_command()))

    routes = _verification_obligations(
        durable_routes, baseline_routes, ambiguous_routes, route_present
    )
    rules = _verification_obligations(
        durable_rules, baseline_rules, ambiguous_rules, rule_present
    )
    manifest = NETWORK.NetworkManifest(tuple(sorted(routes)), tuple(sorted(rules)))
    NETWORK._write_manifest(manifest_path, manifest)
    return manifest


def main(argv: list[str]) -> int:
    if len(argv) != 4 or argv[1] != "snapshot":
        print(
            "usage: tun-package-verification-network.py snapshot <transaction-dir> <manifest>",
            file=sys.stderr,
        )
        return 2
    try:
        snapshot_verification_transactions(Path(argv[2]), Path(argv[3]))
        return 0
    except NETWORK.InspectionError as exc:
        print(f"verification network inspection failed: {exc}", file=sys.stderr)
        return 2
    except (NETWORK.MetadataError, OSError) as exc:
        print(f"verification network metadata rejected: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
