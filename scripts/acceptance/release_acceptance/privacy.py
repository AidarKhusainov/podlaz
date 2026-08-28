from __future__ import annotations

from collections import Counter
from dataclasses import dataclass
from enum import Enum
import ipaddress
import json
import re
from urllib.parse import urlsplit

from .command import CommandRunner
from .model import AmbiguousState, ScenarioOutcome
from .product import RuntimeState


class Probe(str, Enum):
    SUCCESS = "success"
    BLOCKED = "blocked"
    TIMEOUT = "timeout"
    ERROR = "error"


@dataclass(frozen=True)
class PrivacyVerdict:
    outcome: ScenarioOutcome
    reason: str
    direct: Probe
    local_proof: bool


class PrivacyObserver:
    _TABLE_RE = re.compile(r"^podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?$")
    _INTERFACE_RE = re.compile(r"^[A-Za-z0-9_.:-]+$")
    _OWNER_PREFIX = "podlaz:privacy-envelope:"

    def __init__(self, runner: CommandRunner, *, probe_url: str = "https://example.com/"):
        self.runner = runner
        self.probe_url = probe_url
        self.uplink: str | None = None
        self.host: str | None = None
        self.port: int | None = None
        self.ip: str | None = None

    def baseline(self) -> None:
        route = self.runner.run(
            ("ip", "-4", "route", "show", "table", "main", "default"), timeout=5
        ).require_success("default route")
        fields = route.stdout.split()
        if "dev" not in fields:
            raise AmbiguousState("ordinary IPv4 default route has no device")
        uplink = fields[fields.index("dev") + 1]
        if uplink == "podlaz0":
            raise AmbiguousState("ordinary direct-uplink baseline resolved to podlaz0")
        parsed = urlsplit(self.probe_url)
        if parsed.scheme not in {"http", "https"} or not parsed.hostname:
            raise AmbiguousState("invalid acceptance probe URL")
        port = parsed.port or (443 if parsed.scheme == "https" else 80)
        resolved = self.runner.run(("getent", "ahostsv4", parsed.hostname), timeout=10).require_success(
            "resolve direct probe target"
        )
        first = resolved.stdout.splitlines()[0].split()[0] if resolved.stdout.splitlines() else ""
        try:
            parsed_ip = ipaddress.ip_address(first)
        except ValueError as error:
            raise AmbiguousState("direct probe did not resolve one IPv4 target") from error
        if parsed_ip.version != 4:
            raise AmbiguousState("direct probe target is not IPv4")
        self.uplink, self.host, self.port, self.ip = uplink, parsed.hostname, port, str(parsed_ip)
        if self._probe() != Probe.SUCCESS:
            raise AmbiguousState("ordinary direct-egress tripwire baseline is not reachable")

    def observe_protected(self) -> PrivacyVerdict:
        direct = self._probe()
        local = self._local_envelope_proof()
        if direct == Probe.SUCCESS:
            return PrivacyVerdict(ScenarioOutcome.FAIL, "direct_egress_leak", direct, local)
        if local:
            return PrivacyVerdict(ScenarioOutcome.PASS, "direct_blocked_and_local_envelope_exact", direct, True)
        return PrivacyVerdict(ScenarioOutcome.FAIL, "inconclusive_local_privacy_authority", direct, False)

    def observe_ordinary(self) -> PrivacyVerdict:
        direct = self._probe()
        if direct == Probe.SUCCESS:
            return PrivacyVerdict(ScenarioOutcome.PASS, "ordinary_direct_egress_restored", direct, False)
        return PrivacyVerdict(ScenarioOutcome.FAIL, "ordinary_direct_egress_not_restored", direct, False)

    def _probe(self) -> Probe:
        if not all((self.uplink, self.host, self.port, self.ip)):
            raise AmbiguousState("privacy baseline was not established")
        result = self.runner.run(
            (
                "curl", "-4", "-fsS", "--interface", self.uplink,
                "--connect-timeout", "3", "--max-time", "5",
                "--resolve", f"{self.host}:{self.port}:{self.ip}", self.probe_url,
                "-o", "/dev/null",
            ),
            timeout=7,
        )
        if result.returncode == 0:
            return Probe.SUCCESS
        if result.returncode == 28:
            return Probe.TIMEOUT
        return Probe.BLOCKED

    def _local_envelope_proof(self) -> bool:
        try:
            state = RuntimeState.continuation()
            protection = self._validated_authority(state)
        except AmbiguousState:
            return False
        result = self.runner.run(
            ("nft", "-j", "list", "table", protection["family"], protection["table"]),
            timeout=5,
        )
        if result.returncode != 0:
            return False
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError:
            return False
        return self._verify_nft_json(payload, protection)

    def _validated_authority(self, state: dict) -> dict:
        if state.get("schema_version") != "podlaz.network-session-state.v1":
            raise AmbiguousState("unsupported network-session authority schema")
        if state.get("owner") != "podlaz" or state.get("intent") not in {"resume", "terminal"}:
            raise AmbiguousState("network-session authority is not Podlaz-owned")
        session_id = str(state.get("session_id") or "")
        if not re.fullmatch(r"[0-9a-f]{32}", session_id):
            raise AmbiguousState("network-session identity is invalid")
        protection = state.get("protection")
        if not isinstance(protection, dict):
            raise AmbiguousState("network-session has no privacy authority")
        if protection.get("state") not in {"armed", "arming", "removing"}:
            raise AmbiguousState("privacy authority is not protecting a live convergence interval")
        if protection.get("composition_version") != 1:
            raise AmbiguousState("unsupported privacy composition version")
        family = str(protection.get("family") or "")
        table = str(protection.get("table") or "")
        tun = str(protection.get("tun_interface") or "")
        if family != "inet" or not self._TABLE_RE.fullmatch(table):
            raise AmbiguousState("invalid privacy table authority")
        if not self._INTERFACE_RE.fullmatch(tun):
            raise AmbiguousState("invalid privacy TUN authority")
        bootstrap = self._normalize_bootstrap(protection.get("bootstrap_ipv4"))
        previous = self._normalize_bootstrap(protection.get("previous_bootstrap_ipv4"), allow_empty=True)
        return {
            "family": family,
            "table": table,
            "tun_interface": tun,
            "bootstrap_ipv4": bootstrap,
            "previous_bootstrap_ipv4": previous,
        }

    @staticmethod
    def _normalize_bootstrap(value, *, allow_empty: bool = False) -> tuple[str, ...]:
        if value is None:
            value = []
        if not isinstance(value, list):
            raise AmbiguousState("privacy bootstrap authority is not a list")
        normalized: list[str] = []
        for raw in value:
            try:
                address = ipaddress.ip_address(str(raw).strip())
            except ValueError as error:
                raise AmbiguousState("privacy bootstrap authority contains an invalid address") from error
            if address.version != 4:
                raise AmbiguousState("privacy bootstrap authority contains non-IPv4 state")
            normalized.append(str(address))
        if len(normalized) != len(set(normalized)):
            raise AmbiguousState("privacy bootstrap authority contains duplicates")
        if not normalized and not allow_empty:
            raise AmbiguousState("privacy bootstrap authority is empty")
        return tuple(sorted(normalized))

    def _verify_nft_json(self, payload: dict, protection: dict) -> bool:
        items = payload.get("nftables")
        if not isinstance(items, list):
            return False
        family = protection["family"]
        table = protection["table"]
        table_records = []
        chains = []
        rules = []
        for item in items:
            if not isinstance(item, dict):
                return False
            table_record = item.get("table")
            if isinstance(table_record, dict) and table_record.get("family") == family and table_record.get("name") == table:
                table_records.append(table_record)
            chain = item.get("chain")
            if isinstance(chain, dict) and chain.get("family") == family and chain.get("table") == table:
                chains.append(chain)
            rule = item.get("rule")
            if isinstance(rule, dict) and rule.get("family") == family and rule.get("table") == table:
                rules.append(rule)
        if len(table_records) != 1 or len(chains) != 1:
            return False
        chain = chains[0]
        if chain.get("name") != "output" or chain.get("type") != "filter" or chain.get("hook") != "output":
            return False
        try:
            priority = int(chain.get("prio"))
        except (TypeError, ValueError):
            return False
        if priority != -10:
            return False

        expected_owners = Counter({
            self._OWNER_PREFIX + "loopback": 1,
            self._OWNER_PREFIX + "tun-egress": 1,
            self._OWNER_PREFIX + "dhcp4": 1,
            self._OWNER_PREFIX + "dhcp6": 1,
            self._OWNER_PREFIX + "ipv6-link-control": 1,
            self._OWNER_PREFIX + "block-direct": 1,
            self._OWNER_PREFIX + "bootstrap": len(protection["bootstrap_ipv4"]),
        })
        observed_owners = Counter(str(rule.get("comment") or "") for rule in rules)
        if observed_owners != expected_owners:
            return False
        if len(rules) != len(protection["bootstrap_ipv4"]) + 6:
            return False

        bootstrap_seen: list[str] = []
        for rule in rules:
            if rule.get("chain") != "output" or not isinstance(rule.get("expr"), list):
                return False
            owner = str(rule.get("comment") or "")
            serialized = json.dumps(rule["expr"], sort_keys=True, separators=(",", ":"))
            if owner == self._OWNER_PREFIX + "loopback":
                if "oifname" not in serialized or '"lo"' not in serialized or not self._has_verdict(rule, "accept"):
                    return False
            elif owner == self._OWNER_PREFIX + "tun-egress":
                if "oifname" not in serialized or json.dumps(protection["tun_interface"]) not in serialized or not self._has_verdict(rule, "accept"):
                    return False
            elif owner == self._OWNER_PREFIX + "bootstrap":
                endpoint = self._bootstrap_endpoint(rule)
                if endpoint is None or not self._has_verdict(rule, "accept"):
                    return False
                bootstrap_seen.append(endpoint)
            elif owner == self._OWNER_PREFIX + "dhcp4":
                if not all(token in serialized for token in ('"ipv4"', '"udp"', '"sport"', '"dport"', "68", "67")) or not self._has_verdict(rule, "accept"):
                    return False
            elif owner == self._OWNER_PREFIX + "dhcp6":
                if not all(token in serialized for token in ('"ipv6"', '"udp"', '"sport"', '"dport"', "546", "547")) or not self._has_verdict(rule, "accept"):
                    return False
            elif owner == self._OWNER_PREFIX + "ipv6-link-control":
                if not all(token in serialized for token in ('"ipv6"', '"icmpv6"', "nd-router-solicit", "nd-neighbor-solicit", "nd-neighbor-advert")) or not self._has_verdict(rule, "accept"):
                    return False
            elif owner == self._OWNER_PREFIX + "block-direct":
                if not self._has_verdict(rule, "reject"):
                    return False
            else:
                return False
        return tuple(sorted(bootstrap_seen)) == protection["bootstrap_ipv4"]

    @staticmethod
    def _has_verdict(rule: dict, verdict: str) -> bool:
        for expression in rule.get("expr") or []:
            if isinstance(expression, dict) and verdict in expression:
                return True
        return False

    @staticmethod
    def _bootstrap_endpoint(rule: dict) -> str | None:
        for expression in rule.get("expr") or []:
            if not isinstance(expression, dict):
                continue
            match = expression.get("match")
            if not isinstance(match, dict):
                continue
            left = match.get("left")
            right = match.get("right")
            left_text = json.dumps(left, sort_keys=True, separators=(",", ":"))
            if '"payload"' not in left_text or '"ip"' not in left_text or '"daddr"' not in left_text:
                continue
            try:
                address = ipaddress.ip_address(str(right))
            except ValueError:
                return None
            return str(address) if address.version == 4 else None
        return None
