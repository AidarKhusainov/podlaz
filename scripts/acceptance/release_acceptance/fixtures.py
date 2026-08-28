from __future__ import annotations

from dataclasses import dataclass

from .checkpoint import MutationLedger
from .command import CommandRunner
from .model import AmbiguousState


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
        self.ledger.begin_release(s.name)
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
