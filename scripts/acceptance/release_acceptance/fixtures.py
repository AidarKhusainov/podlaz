from __future__ import annotations

from dataclasses import dataclass
import json

from .checkpoint import MutationLedger
from .command import CommandRunner
from .model import AmbiguousState, MutationState


@dataclass(frozen=True)
class FixtureSpec:
    name: str
    tun: str
    cidr: str
    table: str
    route: str
    rule_a: str
    rule_b: str
    priority_a: int
    priority_b: int
    nft_table: str
    dns_link: str
    dns_server: str
    dns_domain: str


FIXTURE_A = FixtureSpec(
    "fixture_a", "podlaz-accept-a0", "198.18.0.1/32", "51820", "198.51.100.254/32",
    "198.51.100.254/32", "198.51.100.253/32", 9999, 10000,
    "podlaz_accept_a", "podlaz-accept-adns0", "192.0.2.53", "~accept-a.invalid",
)
FIXTURE_B = FixtureSpec(
    "fixture_b", "podlaz-accept-b0", "198.18.62.1/32", "51962", "203.0.113.62/32",
    "203.0.113.62/32", "203.0.113.63/32", 10999, 11000,
    "podlaz_accept_b", "podlaz-accept-bdns0", "192.0.2.54", "~accept-b.invalid",
)


class FixtureLease:
    def __init__(self, runner: CommandRunner, ledger: MutationLedger, spec: FixtureSpec):
        self.runner = runner
        self.ledger = ledger
        self.spec = spec
        self.current_route = spec.route

    def acquire(self) -> None:
        self._assert_free()
        identity = self.spec.__dict__.copy()
        identity["current_route"] = self.spec.route
        self.ledger.begin_acquire(self.spec.name, "network_fixture", identity)
        s = self.spec
        self._run("ip", "tuntap", "add", "dev", s.tun, "mode", "tun")
        self._run("ip", "link", "set", "dev", s.tun, "up")
        self._run("ip", "-4", "address", "add", s.cidr, "dev", s.tun)
        self._run("ip", "-4", "route", "add", "blackhole", s.route, "table", s.table)
        self._run("ip", "-4", "rule", "add", "priority", str(s.priority_a), "to", s.rule_a, "lookup", s.table)
        self._run("ip", "-4", "rule", "add", "priority", str(s.priority_b), "to", s.rule_b, "lookup", s.table)
        self._run("nft", "add", "table", "inet", s.nft_table)
        self._run("ip", "link", "add", s.dns_link, "type", "dummy")
        self._run("ip", "link", "set", "dev", s.dns_link, "up")
        self._run("resolvectl", "dns", s.dns_link, s.dns_server)
        self._run("resolvectl", "domain", s.dns_link, s.dns_domain)
        self._run("resolvectl", "default-route", s.dns_link, "no")
        self.verify()
        self.ledger.mark_acquired(self.spec.name)

    def churn(self) -> None:
        s = self.spec
        alternate = "203.0.113.64/32" if s == FIXTURE_B else "198.51.100.252/32"
        self._run("ip", "-4", "route", "replace", "blackhole", alternate, "table", s.table)
        self._run("ip", "-4", "route", "del", "blackhole", self.current_route, "table", s.table)
        self.current_route = alternate
        checkpoint = self.ledger.store.load()
        checkpoint.mutations[s.name].identity["current_route"] = alternate
        self.ledger.store.replace(checkpoint)

    def verify(self) -> None:
        s = self.spec
        checks = [
            (("ip", "link", "show", "dev", s.tun), s.tun),
            (("ip", "-4", "address", "show", "dev", s.tun), s.cidr),
            (("ip", "-4", "route", "show", "table", s.table), self.current_route.split("/", 1)[0]),
            (("nft", "list", "table", "inet", s.nft_table), s.nft_table),
            (("resolvectl", "status", s.dns_link, "--no-pager"), s.dns_server),
        ]
        for argv, token in checks:
            result = self.runner.run(argv, timeout=5)
            if result.returncode != 0 or token not in result.stdout:
                raise AmbiguousState(f"fixture {s.name} exact composition drifted")

    def release(self) -> None:
        s = self.spec
        checkpoint = self.ledger.store.load()
        record = checkpoint.mutations.get(s.name)
        if record is None:
            raise AmbiguousState(f"fixture {s.name} has no persisted authority")
        self.current_route = str(record.identity.get("current_route") or s.route)
        self.verify()
        if record.state == MutationState.ACQUIRED:
            self.ledger.begin_release(s.name)
        elif record.state != MutationState.RELEASING:
            raise AmbiguousState(
                f"fixture {s.name} cannot use full release from {record.state.value}"
            )
        commands = [
            ("resolvectl", "revert", s.dns_link),
            ("ip", "link", "del", "dev", s.dns_link),
            ("nft", "delete", "table", "inet", s.nft_table),
            ("ip", "-4", "rule", "del", "priority", str(s.priority_b), "to", s.rule_b, "lookup", s.table),
            ("ip", "-4", "rule", "del", "priority", str(s.priority_a), "to", s.rule_a, "lookup", s.table),
            ("ip", "-4", "route", "del", "blackhole", self.current_route, "table", s.table),
            ("ip", "link", "del", "dev", s.tun),
        ]
        for argv in commands:
            result = self.runner.run(argv, timeout=10)
            if result.returncode != 0:
                raise AmbiguousState(f"could not release exact fixture {s.name}: {' '.join(argv)}")
        self.ledger.mark_released(s.name)

    def release_partial(self) -> None:
        """Release only components that can be proven to match an interrupted acquire.

        This path is intentionally conservative.  It is valid only for write-ahead
        ACQUIRING authority.  Any occupied identity that is not exactly one of the
        fixture components aborts cleanup instead of deleting potentially foreign
        network state.
        """
        s = self.spec
        checkpoint = self.ledger.store.load()
        record = checkpoint.mutations.get(s.name)
        if record is None or record.kind != "network_fixture":
            raise AmbiguousState(f"fixture {s.name} has no persisted network authority")
        if record.state != MutationState.ACQUIRING:
            raise AmbiguousState(
                f"fixture {s.name} partial release requires acquiring authority"
            )
        self.current_route = str(record.identity.get("current_route") or s.route)

        tun_present = self._observe_link(s.tun, "tun")
        dns_present = self._observe_link(s.dns_link, "dummy")
        route_present = self._observe_route()
        rule_a_present = self._observe_rule(s.priority_a, s.rule_a)
        rule_b_present = self._observe_rule(s.priority_b, s.rule_b)
        nft_present = self._observe_nft_table()

        commands: list[tuple[str, ...]] = []
        if dns_present:
            commands.extend(
                [
                    ("resolvectl", "revert", s.dns_link),
                    ("ip", "link", "del", "dev", s.dns_link),
                ]
            )
        if nft_present:
            commands.append(("nft", "delete", "table", "inet", s.nft_table))
        if rule_b_present:
            commands.append(
                ("ip", "-4", "rule", "del", "priority", str(s.priority_b), "to", s.rule_b, "lookup", s.table)
            )
        if rule_a_present:
            commands.append(
                ("ip", "-4", "rule", "del", "priority", str(s.priority_a), "to", s.rule_a, "lookup", s.table)
            )
        if route_present:
            commands.append(
                ("ip", "-4", "route", "del", "blackhole", self.current_route, "table", s.table)
            )
        if tun_present:
            commands.append(("ip", "link", "del", "dev", s.tun))

        for argv in commands:
            result = self.runner.run(argv, timeout=10)
            if result.returncode != 0:
                raise AmbiguousState(
                    f"could not release exact partial fixture {s.name}: {' '.join(argv)}"
                )
        self.ledger.mark_released(s.name)

    def _observe_link(self, name: str, expected_kind: str) -> bool:
        result = self.runner.run(
            ("ip", "-j", "-d", "link", "show", "dev", name), timeout=5
        )
        if result.returncode != 0:
            return False
        try:
            payload = json.loads(result.stdout or "[]")
        except json.JSONDecodeError as error:
            raise AmbiguousState(f"fixture {self.spec.name} link evidence is invalid") from error
        if not isinstance(payload, list) or len(payload) != 1:
            if payload == []:
                return False
            raise AmbiguousState(f"fixture {self.spec.name} link evidence is ambiguous")
        link = payload[0]
        if not isinstance(link, dict) or link.get("ifname") != name:
            raise AmbiguousState(f"fixture {self.spec.name} link identity changed")
        linkinfo = link.get("linkinfo")
        kind = linkinfo.get("info_kind") if isinstance(linkinfo, dict) else None
        if kind != expected_kind:
            raise AmbiguousState(
                f"fixture {self.spec.name} link {name} has foreign kind {kind!r}"
            )
        return True

    def _observe_route(self) -> bool:
        s = self.spec
        result = self.runner.run(
            ("ip", "-j", "-4", "route", "show", "table", s.table), timeout=5
        )
        if result.returncode != 0:
            return False
        try:
            payload = json.loads(result.stdout or "[]")
        except json.JSONDecodeError as error:
            raise AmbiguousState(f"fixture {s.name} route evidence is invalid") from error
        if not isinstance(payload, list):
            raise AmbiguousState(f"fixture {s.name} route evidence is ambiguous")
        if not payload:
            return False
        if len(payload) != 1 or not isinstance(payload[0], dict):
            raise AmbiguousState(f"fixture routing table {s.table} contains foreign state")
        route = payload[0]
        if (
            route.get("type") != "blackhole"
            or route.get("dst") != self.current_route
            or str(route.get("table")) != s.table
        ):
            raise AmbiguousState(f"fixture routing table {s.table} contains foreign state")
        return True

    def _observe_rule(self, priority: int, target: str) -> bool:
        s = self.spec
        result = self.runner.run(
            ("ip", "-4", "rule", "show", "priority", str(priority)), timeout=5
        )
        if result.returncode != 0:
            return False
        lines = [line.strip() for line in result.stdout.splitlines() if line.strip()]
        if not lines:
            return False
        expected_prefix = f"{priority}:"
        expected_body = f"to {target} lookup {s.table}"
        if len(lines) != 1 or not lines[0].startswith(expected_prefix) or expected_body not in lines[0]:
            raise AmbiguousState(f"fixture rule priority {priority} contains foreign state")
        return True

    def _observe_nft_table(self) -> bool:
        s = self.spec
        result = self.runner.run(
            ("nft", "-j", "list", "table", "inet", s.nft_table), timeout=5
        )
        if result.returncode != 0:
            return False
        try:
            payload = json.loads(result.stdout or "{}")
        except json.JSONDecodeError as error:
            raise AmbiguousState(f"fixture {s.name} nft evidence is invalid") from error
        entries = payload.get("nftables") if isinstance(payload, dict) else None
        if not isinstance(entries, list):
            raise AmbiguousState(f"fixture {s.name} nft evidence is ambiguous")
        tables = []
        foreign = []
        for entry in entries:
            if not isinstance(entry, dict):
                foreign.append(entry)
                continue
            if "metainfo" in entry:
                continue
            if set(entry) == {"table"} and isinstance(entry["table"], dict):
                tables.append(entry["table"])
            else:
                foreign.append(entry)
        if foreign or len(tables) != 1:
            raise AmbiguousState(f"fixture nft table {s.nft_table} contains foreign state")
        table = tables[0]
        if table.get("family") != "inet" or table.get("name") != s.nft_table:
            raise AmbiguousState(f"fixture nft table {s.nft_table} identity changed")
        return True

    def _assert_free(self) -> None:
        s = self.spec
        probes = (
            ("ip", "link", "show", "dev", s.tun),
            ("ip", "link", "show", "dev", s.dns_link),
            ("nft", "list", "table", "inet", s.nft_table),
            ("ip", "-4", "rule", "show", "priority", str(s.priority_a)),
            ("ip", "-4", "rule", "show", "priority", str(s.priority_b)),
        )
        for argv in probes:
            result = self.runner.run(argv, timeout=5)
            if result.returncode == 0 and result.stdout.strip():
                raise AmbiguousState(f"fixture identity is already occupied: {' '.join(argv)}")
        routes = self.runner.run(("ip", "-4", "route", "show", "table", s.table), timeout=5)
        if routes.returncode == 0 and routes.stdout.strip():
            raise AmbiguousState(f"fixture routing table {s.table} is already occupied")

    def _run(self, *argv: str) -> None:
        self.runner.run(argv, timeout=10).require_success(f"fixture {self.spec.name}: {' '.join(argv)}")
