from __future__ import annotations

import json
from pathlib import Path
import re

from .command import CommandRunner, CommandResult
from .model import AcceptanceError, AmbiguousState, UserIdentity


TERMINAL_PROFILE_URI = (
    "vless://00000000-0000-4000-8000-000000000001@vpn.invalid:443"
    "?security=tls&type=tcp&sni=vpn.invalid#ReleaseAcceptanceFailure"
)
TERMINAL_PROFILE_NAME = "ReleaseAcceptanceFailure"
TERMINAL_PROFILE_SERVER = "vpn.invalid"
TERMINAL_PROFILE_PORT = 443
TERMINAL_PROFILE_PROTOCOL = "vless"


class ProductClient:
    def __init__(self, runner: CommandRunner, user: UserIdentity):
        self.runner = runner
        self.user = user

    def run(self, *args: str, timeout: float = 30) -> CommandResult:
        return self.runner.run(("/usr/bin/podlaz", *args), timeout=timeout, user=self.user)

    def profile_ids(self) -> set[str]:
        result = self.run("profile", "list", "--json")
        result.require_success("profile list")
        payload = json.loads(result.stdout)
        if payload.get("schema_version") != "v1":
            raise AmbiguousState("unsupported profile-list JSON schema")
        return {
            str(item["id"])
            for item in payload.get("profiles", [])
            if isinstance(item, dict) and item.get("id")
        }

    def validate_tun_profile(self, profile_id: str) -> None:
        result = self.run("profile", "validate", profile_id, "--mode", "tun", "--json")
        if result.returncode != 0:
            raise AcceptanceError(f"profile {profile_id!r} is not valid for TUN mode")
        payload = json.loads(result.stdout)
        if payload.get("schema_version") != "v1" or payload.get("valid") is not True:
            raise AmbiguousState("profile validation did not return valid v1 evidence")

    def select_profile(self, explicit: str | None) -> str:
        ids = self.profile_ids()
        if explicit:
            if explicit not in ids:
                raise AcceptanceError("requested profile does not exist")
            self.validate_tun_profile(explicit)
            return explicit
        valid = []
        for profile_id in sorted(ids):
            try:
                self.validate_tun_profile(profile_id)
            except AcceptanceError:
                continue
            valid.append(profile_id)
        if len(valid) != 1:
            raise AmbiguousState(f"expected exactly one usable TUN profile, found {len(valid)}")
        return valid[0]

    def connect_tun(self, profile_id: str) -> None:
        self.run("connect", "--mode", "tun", profile_id, timeout=90).require_success("Podlaz TUN connect")

    def disconnect(self) -> None:
        self.run("disconnect", timeout=90).require_success("Podlaz disconnect")

    def doctor_tun(self) -> CommandResult:
        return self.run("doctor", "--tun", "--json", timeout=45)

    def autostart_status(self) -> str:
        result = self.run("autostart", "status").require_success("autostart status")
        first = result.stdout.splitlines()[0].strip() if result.stdout else ""
        if first not in {"Autostart: Disabled", "Autostart: Enabled for next boot"}:
            if not first.startswith("Autostart: Enabled for next boot"):
                raise AmbiguousState("unrecognized autostart status")
        return first

    def autostart_enable(self, profile_id: str) -> None:
        self.run("autostart", "enable", "--mode", "tun", profile_id).require_success("enable autostart")

    def autostart_disable(self) -> None:
        self.run("autostart", "disable").require_success("disable autostart")

    def create_terminal_profile(self) -> str:
        baseline = self.profile_ids()
        result = self.run("profile", "import", TERMINAL_PROFILE_URI).require_success(
            "create terminal acceptance profile"
        )
        match = re.search(r"^Imported profile:\s+(\S+)\s*$", result.stdout, re.M)
        after = self.profile_ids()
        additions = after - baseline
        if match and match.group(1) in additions and len(additions) == 1:
            profile_id = match.group(1)
        elif len(additions) == 1:
            profile_id = next(iter(additions))
        else:
            raise AmbiguousState("could not acquire exact terminal profile identity")
        if not self.is_terminal_acceptance_profile(profile_id):
            raise AmbiguousState("created terminal profile does not match acceptance fixture")
        self.validate_tun_profile(profile_id)
        return profile_id

    def is_terminal_acceptance_profile(self, profile_id: str) -> bool:
        result = self.run("profile", "show", profile_id, "--json")
        if result.returncode != 0:
            return False
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError:
            return False
        if payload.get("schema_version") != "v1" or payload.get("status") != "ok":
            return False
        profile = payload.get("profile")
        if not isinstance(profile, dict):
            return False

        def value(name: str):
            return profile.get(name, profile.get(name[:1].upper() + name[1:]))

        try:
            port = int(value("port"))
        except (TypeError, ValueError):
            return False
        return (
            str(value("name") or "") == TERMINAL_PROFILE_NAME
            and str(value("server") or "") == TERMINAL_PROFILE_SERVER
            and port == TERMINAL_PROFILE_PORT
            and str(value("protocol") or "").lower() == TERMINAL_PROFILE_PROTOCOL
        )

    def delete_profile(self, profile_id: str) -> None:
        self.run("profile", "delete", profile_id, "--yes").require_success("delete terminal acceptance profile")


class RuntimeState:
    CONTINUATION = Path("/run/podlaz/network-session-continuation.json")
    TRANSACTIONS = Path("/run/podlaz/transactions")
    BOOT_ATTEMPT = Path("/run/podlaz/boot-autostart-attempt.json")

    @staticmethod
    def boot_id() -> str:
        value = Path("/proc/sys/kernel/random/boot_id").read_text(encoding="utf-8").strip()
        if not value:
            raise AmbiguousState("empty Linux boot_id")
        return value

    @classmethod
    def continuation(cls) -> dict:
        return cls._load_private_json(cls.CONTINUATION)

    @classmethod
    def boot_attempt(cls) -> dict:
        return cls._load_private_json(cls.BOOT_ATTEMPT)

    @staticmethod
    def _load_private_json(path: Path) -> dict:
        if path.is_symlink() or not path.is_file():
            raise AmbiguousState(f"missing private runtime state: {path}")
        value = json.loads(path.read_text(encoding="utf-8"))
        if not isinstance(value, dict):
            raise AmbiguousState(f"invalid private runtime state: {path}")
        return value
