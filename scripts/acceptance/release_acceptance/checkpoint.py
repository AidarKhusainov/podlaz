from __future__ import annotations

from dataclasses import asdict
import json
import os
from pathlib import Path
import tempfile
from typing import Callable

from .model import AmbiguousState, Checkpoint, MutationRecord, MutationState, ScenarioOutcome, ScenarioRecord


class CheckpointStore:
    def __init__(self, path: Path):
        self.path = path

    def exists(self) -> bool:
        return self.path.is_file() and not self.path.is_symlink()

    def load(self) -> Checkpoint:
        if self.path.is_symlink() or not self.path.is_file():
            raise AmbiguousState("checkpoint is missing or not a regular file")
        if self.path.stat().st_size > 1024 * 1024:
            raise AmbiguousState("checkpoint is too large")
        raw = json.loads(self.path.read_text(encoding="utf-8"))
        mutations = {
            name: MutationRecord(MutationState(value["state"]), value["kind"], dict(value.get("identity") or {}))
            for name, value in (raw.get("mutations") or {}).items()
        }
        scenarios = {
            name: ScenarioRecord(name, ScenarioOutcome(value["outcome"]), value.get("reason", ""), dict(value.get("evidence") or {}))
            for name, value in (raw.get("scenarios") or {}).items()
        }
        return Checkpoint(
            schema_version=raw["schema_version"],
            run_id=raw["run_id"],
            phase=raw["phase"],
            user=dict(raw["user"]),
            candidate=dict(raw["candidate"]),
            previous_boot_id=raw["previous_boot_id"],
            original_autostart=dict(raw.get("original_autostart") or {}),
            mutations=mutations,
            scenarios=scenarios,
            private=dict(raw.get("private") or {}),
        )

    def replace(self, checkpoint: Checkpoint) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        payload = json.dumps(asdict(checkpoint), indent=2, sort_keys=True, default=lambda value: value.value) + "\n"
        fd, tmp_name = tempfile.mkstemp(prefix=f".{self.path.name}.", dir=self.path.parent)
        try:
            os.fchmod(fd, 0o600)
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                handle.write(payload)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(tmp_name, self.path)
            directory_fd = os.open(self.path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory_fd)
            finally:
                os.close(directory_fd)
        finally:
            try:
                os.unlink(tmp_name)
            except FileNotFoundError:
                pass

    def remove(self) -> None:
        try:
            self.path.unlink()
        except FileNotFoundError:
            return
        directory_fd = os.open(self.path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)


class MutationLedger:
    def __init__(self, store: CheckpointStore):
        self.store = store

    def begin_acquire(self, name: str, kind: str, identity: dict) -> MutationRecord:
        checkpoint = self.store.load()
        current = checkpoint.mutations.get(name)
        if current and current.state != MutationState.RELEASED:
            raise AmbiguousState(f"mutation {name} already owns authority")
        record = MutationRecord(MutationState.ACQUIRING, kind, identity)
        checkpoint.mutations[name] = record
        self.store.replace(checkpoint)
        return record

    def mark_acquired(self, name: str) -> None:
        self._transition(name, MutationState.ACQUIRING, MutationState.ACQUIRED)

    def begin_release(self, name: str) -> None:
        self._transition(name, MutationState.ACQUIRED, MutationState.RELEASING)

    def mark_released(self, name: str) -> None:
        checkpoint = self.store.load()
        record = checkpoint.mutations[name]
        if record.state not in {MutationState.RELEASING, MutationState.ACQUIRING}:
            raise AmbiguousState(f"mutation {name} cannot become released from {record.state.value}")
        record.state = MutationState.RELEASED
        self.store.replace(checkpoint)

    def reconcile(self, name: str, inspect: Callable[[MutationRecord], str]) -> MutationRecord:
        checkpoint = self.store.load()
        record = checkpoint.mutations[name]
        live = inspect(record)
        if live not in {"absent", "exact"}:
            raise AmbiguousState(f"mutation {name} live state is ambiguous")
        if record.state == MutationState.ACQUIRING:
            record.state = MutationState.ACQUIRED if live == "exact" else MutationState.RELEASED
        elif record.state == MutationState.RELEASING and live == "absent":
            record.state = MutationState.RELEASED
        elif record.state == MutationState.ACQUIRED and live != "exact":
            raise AmbiguousState(f"acquired mutation {name} disappeared")
        self.store.replace(checkpoint)
        return record

    def _transition(self, name: str, expected: MutationState, target: MutationState) -> None:
        checkpoint = self.store.load()
        record = checkpoint.mutations[name]
        if record.state != expected:
            raise AmbiguousState(f"mutation {name}: expected {expected.value}, got {record.state.value}")
        record.state = target
        self.store.replace(checkpoint)
