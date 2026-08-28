from __future__ import annotations

from dataclasses import dataclass
import os
import subprocess
from typing import Mapping, Sequence

from .model import AcceptanceError, UserIdentity


FORBIDDEN_PREFIXES = (
    ("apt",), ("apt-get",),
    ("ip", "route", "flush"), ("ip", "rule", "flush"),
    ("nft", "flush", "ruleset"),
    ("podlaz", "recover", "--execute"),
    ("/usr/bin/podlaz", "recover", "--execute"),
)


@dataclass(frozen=True)
class CommandResult:
    argv: tuple[str, ...]
    returncode: int
    stdout: str
    stderr: str

    def require_success(self, what: str) -> "CommandResult":
        if self.returncode != 0:
            raise AcceptanceError(f"{what} failed with exit {self.returncode}: {self.stderr.strip()}")
        return self


class CommandRunner:
    def __init__(self, *, evidence=None):
        self.evidence = evidence

    def run(
        self,
        argv: Sequence[str],
        *,
        timeout: float,
        user: UserIdentity | None = None,
        env: Mapping[str, str] | None = None,
        input_text: str | None = None,
    ) -> CommandResult:
        if not argv:
            raise ValueError("empty command")
        raw = tuple(str(value) for value in argv)
        self._reject_forbidden(raw)
        command = list(raw)
        run_env = dict(os.environ)
        if env:
            run_env.update(env)
        if user is not None:
            user_env = {
                "HOME": str(user.home),
                "XDG_CONFIG_HOME": str(user.config_home),
                "XDG_STATE_HOME": str(user.state_home),
                "XDG_CACHE_HOME": str(user.cache_home),
            }
            if env:
                user_env.update(env)
            command = ["runuser", "-u", user.name, "--", "env", *[f"{k}={v}" for k, v in user_env.items()], *raw]
        completed = subprocess.run(
            command,
            text=True,
            input=input_text,
            capture_output=True,
            timeout=timeout,
            check=False,
            env=run_env,
        )
        result = CommandResult(raw, completed.returncode, completed.stdout, completed.stderr)
        if self.evidence is not None:
            self.evidence.record_command(result)
        return result

    @staticmethod
    def _reject_forbidden(argv: tuple[str, ...]) -> None:
        normalized = tuple(value.strip() for value in argv)
        for prefix in FORBIDDEN_PREFIXES:
            if normalized[: len(prefix)] == prefix:
                raise AcceptanceError(f"forbidden acceptance command: {' '.join(prefix)}")
        if normalized[:2] == ("systemctl", "restart") and len(normalized) > 2 and normalized[2] in {"NetworkManager", "NetworkManager.service", "systemd-resolved", "systemd-resolved.service"}:
            raise AcceptanceError(f"forbidden network repair command: {' '.join(normalized[:3])}")
