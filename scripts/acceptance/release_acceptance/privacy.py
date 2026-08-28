from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
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
    def __init__(self, runner: CommandRunner, *, probe_url: str = "https://example.com/"):
        self.runner = runner
        self.probe_url = probe_url
        self.uplink: str | None = None
        self.host: str | None = None
        self.port: int | None = None
        self.ip: str | None = None

    def baseline(self) -> None:
        route = self.runner.run(("ip", "-4", "route", "show", "table", "main", "default"), timeout=5).require_success("default route")
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
        resolved = self.runner.run(("getent", "ahostsv4", parsed.hostname), timeout=10).require_success("resolve direct probe target")
        first = resolved.stdout.splitlines()[0].split()[0] if resolved.stdout.splitlines() else ""
        if not re.fullmatch(r"(?:\d{1,3}\.){3}\d{1,3}", first):
            raise AmbiguousState("direct probe did not resolve one IPv4 target")
        self.uplink, self.host, self.port, self.ip = uplink, parsed.hostname, port, first
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
        result = self.runner.run((
            "curl", "-4", "-fsS", "--interface", self.uplink,
            "--connect-timeout", "3", "--max-time", "5",
            "--resolve", f"{self.host}:{self.port}:{self.ip}", self.probe_url,
            "-o", "/dev/null",
        ), timeout=7)
        if result.returncode == 0:
            return Probe.SUCCESS
        if result.returncode == 28:
            return Probe.TIMEOUT
        return Probe.BLOCKED

    def _local_envelope_proof(self) -> bool:
        try:
            continuation = RuntimeState.continuation()
        except AmbiguousState:
            return False
        protection = continuation.get("protection") or {}
        if protection.get("state") not in {"armed", "arming"}:
            return False
        family = str(protection.get("family") or "")
        table = str(protection.get("table") or "")
        if family != "inet" or not re.fullmatch(r"podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?", table):
            return False
        observed = self.runner.run(("nft", "-y", "list", "table", family, table), timeout=5)
        if observed.returncode != 0:
            return False
        text = observed.stdout
        if f"table {family} {table}" not in text:
            return False
        return "podlaz:privacy-envelope:" in text and "policy drop" in text
