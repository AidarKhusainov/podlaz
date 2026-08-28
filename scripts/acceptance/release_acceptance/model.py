from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from pathlib import Path
from typing import Any


class RunMode(str, Enum):
    NEW = "new"
    RESUME = "resume"
    ABORT = "abort"


class MutationState(str, Enum):
    ACQUIRING = "acquiring"
    ACQUIRED = "acquired"
    RELEASING = "releasing"
    RELEASED = "released"


class ScenarioOutcome(str, Enum):
    PASS = "PASS"
    FAIL = "FAIL"
    SKIP_HOST_CAPABILITY = "SKIP_HOST_CAPABILITY"
    SKIP_RELEASE_CAPABILITY = "SKIP_RELEASE_CAPABILITY"
    SKIP_REMOTE_SESSION = "SKIP_REMOTE_SESSION"
    SKIP_USER_REQUEST = "SKIP_USER_REQUEST"
    NOT_EXERCISED = "NOT_EXERCISED"
    INCONCLUSIVE = "INCONCLUSIVE"


class Qualification(str, Enum):
    QUALIFIED_PASS = "QUALIFIED_PASS"
    PARTIAL_PASS = "PARTIAL_PASS"
    FAIL = "FAIL"


@dataclass(frozen=True)
class UserIdentity:
    name: str
    uid: int
    gid: int
    home: Path

    @property
    def config_home(self) -> Path:
        return self.home / ".config"

    @property
    def state_home(self) -> Path:
        return self.home / ".local" / "state"

    @property
    def cache_home(self) -> Path:
        return self.home / ".cache"


@dataclass(frozen=True)
class RunConfig:
    mode: RunMode
    candidate: Path | None = None
    previous_deb: Path | None = None
    profile: str | None = None
    artifact_dir: Path | None = None
    soak_minutes: int = 60
    allow_wifi_reconnect: bool = True
    allow_suspend: bool = True
    reboot_phases: bool = True


@dataclass(frozen=True)
class DebIdentity:
    path: Path
    package: str
    version: str
    architecture: str
    sha256: str
    device: int
    inode: int


@dataclass
class MutationRecord:
    state: MutationState
    kind: str
    identity: dict[str, Any]


@dataclass
class ScenarioRecord:
    name: str
    outcome: ScenarioOutcome
    reason: str = ""
    evidence: dict[str, Any] = field(default_factory=dict)


@dataclass
class Checkpoint:
    schema_version: str
    run_id: str
    phase: str
    user: dict[str, Any]
    candidate: dict[str, Any]
    previous_boot_id: str
    original_autostart: dict[str, Any] = field(default_factory=dict)
    mutations: dict[str, MutationRecord] = field(default_factory=dict)
    scenarios: dict[str, ScenarioRecord] = field(default_factory=dict)
    private: dict[str, Any] = field(default_factory=dict)


class AcceptanceError(RuntimeError):
    pass


class PreflightError(AcceptanceError):
    pass


class AmbiguousState(AcceptanceError):
    pass


class UnsafePath(AcceptanceError):
    pass
