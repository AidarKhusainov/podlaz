from __future__ import annotations

import json
import os
import tempfile
import unittest
from pathlib import Path

from scripts.e2e.lib import tun_soak_analysis, tun_soak_metrics


class TunSoakMetricsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name)
        self.proc = self.root / "proc"
        self.cgroup = self.root / "cgroup"
        self.transactions = self.root / "transactions"
        self.proc.mkdir()
        self.cgroup.mkdir()
        self.transactions.mkdir()

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def write_process(
        self,
        pid: int,
        *,
        exe: str,
        comm: str,
        cgroup_path: str,
        start_time: int,
        rss_kib: int,
        pss_kib: int,
        threads: int,
        fds: int,
        tasks: int,
        utime: int = 10,
        stime: int = 5,
        parent_pid: int = 1,
        cmdline: tuple[str, ...] = (),
        fd_targets: tuple[str, ...] | None = None,
    ) -> None:
        process = self.proc / str(pid)
        (process / "fd").mkdir(parents=True)
        (process / "task").mkdir()
        targets = fd_targets if fd_targets is not None else tuple("/dev/null" for _ in range(fds))
        if len(targets) != fds:
            raise ValueError("fd target count does not match fds")
        for index, target in enumerate(targets):
            (process / "fd" / str(index)).symlink_to(target)
        for index in range(tasks):
            (process / "task" / str(index + 1)).mkdir()
        (process / "exe").symlink_to(exe)
        (process / "comm").write_text(comm + "\n", encoding="utf-8")
        (process / "cmdline").write_bytes(b"\0".join(value.encode() for value in cmdline) + b"\0")
        (process / "cgroup").write_text(f"0::{cgroup_path}\n", encoding="utf-8")
        (process / "status").write_text(
            f"Name:\t{comm}\nVmRSS:\t{rss_kib} kB\nThreads:\t{threads}\n",
            encoding="utf-8",
        )
        (process / "smaps_rollup").write_text(
            f"Rss:\t{rss_kib} kB\nPss:\t{pss_kib} kB\n",
            encoding="utf-8",
        )
        # fields 3..52 after the parenthesized comm; field 14/15 are CPU ticks,
        # field 22 is process start time.
        rest = ["S"] + ["0"] * 49
        rest[1] = str(parent_pid)
        rest[11] = str(utime)
        rest[12] = str(stime)
        rest[19] = str(start_time)
        (process / "stat").write_text(
            f"{pid} ({comm} worker) {' '.join(rest)}\n",
            encoding="utf-8",
        )

    def rewrite_parent_pid(self, pid: int, parent_pid: int) -> None:
        stat_path = self.proc / str(pid) / "stat"
        text = stat_path.read_text(encoding="utf-8").rstrip("\n")
        close = text.rfind(")")
        fields = text[close + 1 :].strip().split()
        fields[1] = str(parent_pid)
        stat_path.write_text(f"{text[: close + 1]} {' '.join(fields)}\n", encoding="utf-8")

    def write_cgroup(self, relative: str) -> None:
        path = self.cgroup / relative.lstrip("/")
        path.mkdir(parents=True)
        (path / "memory.current").write_text("104857600\n", encoding="utf-8")
        (path / "memory.peak").write_text("125829120\n", encoding="utf-8")
        (path / "pids.current").write_text("37\n", encoding="utf-8")
        (path / "cpu.stat").write_text("usage_usec 123456\nuser_usec 100000\nsystem_usec 23456\n", encoding="utf-8")

    def write_transaction(self, pid: int, config_ref: str) -> None:
        payload = {
            "schema_version": "1",
            "owner": "podlaz",
            "id": "private-transaction-id",
            "mode": "tun",
            "state": "committed",
            "rollback": {
                "child_processes": [
                    {
                        "pid": pid,
                        "label": "xray",
                        "config_ref": config_ref,
                        "owner": "podlaz",
                    }
                ]
            },
        }
        (self.transactions / "private-transaction-id.json").write_text(
            json.dumps(payload), encoding="utf-8"
        )


    def test_discovers_inactive_daemon_without_public_identity(self) -> None:
        cgroup_path = "/system.slice/podlazd.service"
        self.write_cgroup(cgroup_path)
        self.write_process(
            101,
            exe="/usr/bin/podlazd",
            comm="podlazd",
            cgroup_path=cgroup_path,
            start_time=1001,
            rss_kib=32000,
            pss_kib=30000,
            threads=8,
            fds=12,
            tasks=8,
        )

        daemon = tun_soak_metrics.discover_daemon_identity(
            daemon_pid=101,
            proc_root=self.proc,
            cgroup_root=self.cgroup,
            daemon_exe="/usr/bin/podlazd",
            expected_cgroup_suffix="/podlazd.service",
        )
        sample = tun_soak_metrics.collect_daemon_boundary_sample(
            daemon,
            proc_root=self.proc,
            cgroup_root=self.cgroup,
            phase="inactive-baseline",
            sample_index=0,
            elapsed_seconds=0,
        )

        encoded = json.dumps(sample, sort_keys=True)
        self.assertNotIn("101", encoded)
        self.assertIsNone(sample["xray"])
        self.assertEqual(12, sample["podlazd"]["fds"])

    def test_discovers_exact_supervised_child_and_emits_no_private_identity(self) -> None:
        cgroup_path = "/system.slice/podlazd.service"
        config_ref = "/run/podlaz/generated/xray.json"
        self.write_cgroup(cgroup_path)
        self.write_process(
            101,
            exe="/usr/bin/podlazd",
            comm="podlazd",
            cgroup_path=cgroup_path,
            start_time=1001,
            rss_kib=32000,
            pss_kib=30000,
            threads=8,
            fds=12,
            tasks=8,
        )
        self.write_process(
            202,
            exe="/usr/lib/podlaz/xray",
            comm="xray",
            cgroup_path=cgroup_path,
            start_time=2002,
            rss_kib=180000,
            pss_kib=170000,
            threads=27,
            fds=21,
            tasks=27,
            parent_pid=101,
            cmdline=("/usr/lib/podlaz/xray", "run", "-config", config_ref),
        )
        self.write_transaction(202, config_ref)

        identity = tun_soak_metrics.discover_active_identity(
            daemon_pid=101,
            transaction_dir=self.transactions,
            proc_root=self.proc,
            cgroup_root=self.cgroup,
            daemon_exe="/usr/bin/podlazd",
            xray_exe="/usr/lib/podlaz/xray",
            expected_cgroup_suffix="/podlazd.service",
        )
        sample = tun_soak_metrics.collect_sample(
            identity,
            proc_root=self.proc,
            cgroup_root=self.cgroup,
            phase="active",
            session=1,
            sample_index=0,
            elapsed_seconds=0,
        )

        encoded = json.dumps(sample, sort_keys=True)
        self.assertNotIn("101", encoded)
        self.assertNotIn("202", encoded)
        self.assertNotIn("private-transaction-id", encoded)
        self.assertNotIn(config_ref, encoded)
        self.assertEqual(32_000 * 1024, sample["podlazd"]["rss_bytes"])
        self.assertEqual(170_000 * 1024, sample["xray"]["pss_bytes"])
        self.assertEqual(37, sample["cgroup"]["pids_current"])
        self.assertEqual(15, sample["podlazd"]["cpu_time_ticks"])

    def test_samples_only_structural_file_descriptor_categories(self) -> None:
        cgroup_path = "/system.slice/podlazd.service"
        self.write_cgroup(cgroup_path)
        self.write_process(
            101,
            exe="/usr/bin/podlazd",
            comm="podlazd",
            cgroup_path=cgroup_path,
            start_time=1001,
            rss_kib=32000,
            pss_kib=30000,
            threads=8,
            fds=6,
            tasks=8,
            fd_targets=(
                "socket:[101]",
                "socket:[102]",
                "pipe:[201]",
                "anon_inode:[eventpoll]",
                "/private/runtime/file",
                "unclassified-private-target",
            ),
        )

        daemon = tun_soak_metrics.discover_daemon_identity(
            daemon_pid=101,
            proc_root=self.proc,
            cgroup_root=self.cgroup,
            daemon_exe="/usr/bin/podlazd",
            expected_cgroup_suffix="/podlazd.service",
        )
        sample = tun_soak_metrics.collect_daemon_boundary_sample(
            daemon,
            proc_root=self.proc,
            cgroup_root=self.cgroup,
            phase="inactive-baseline",
            sample_index=0,
            elapsed_seconds=0,
        )

        metrics = sample["podlazd"]
        self.assertEqual(6, metrics["fds"])
        self.assertEqual(2, metrics["socket_fds"])
        self.assertEqual(1, metrics["pipe_fds"])
        self.assertEqual(1, metrics["anon_inode_fds"])
        self.assertEqual(1, metrics["regular_fds"])
        self.assertEqual(1, metrics["other_fds"])
        encoded = json.dumps(sample, sort_keys=True)
        self.assertNotIn("private/runtime", encoded)
        self.assertNotIn("unclassified-private-target", encoded)
        self.assertNotIn("socket:[", encoded)

    def test_rejects_transaction_child_not_parented_by_daemon(self) -> None:
        cgroup_path = "/system.slice/podlazd.service"
        config_ref = "/run/podlaz/generated/xray.json"
        self.write_cgroup(cgroup_path)
        self.write_process(
            101,
            exe="/usr/bin/podlazd",
            comm="podlazd",
            cgroup_path=cgroup_path,
            start_time=1001,
            rss_kib=32000,
            pss_kib=30000,
            threads=8,
            fds=12,
            tasks=8,
        )
        self.write_process(
            202,
            exe="/usr/lib/podlaz/xray",
            comm="xray",
            cgroup_path=cgroup_path,
            start_time=2002,
            rss_kib=180000,
            pss_kib=170000,
            threads=27,
            fds=21,
            tasks=27,
            parent_pid=404,
            cmdline=("/usr/lib/podlaz/xray", "run", "-config", config_ref),
        )
        self.write_transaction(202, config_ref)

        with self.assertRaisesRegex(tun_soak_metrics.AttributionError, "not parented by podlazd"):
            tun_soak_metrics.discover_active_identity(
                daemon_pid=101,
                transaction_dir=self.transactions,
                proc_root=self.proc,
                cgroup_root=self.cgroup,
                daemon_exe="/usr/bin/podlazd",
                xray_exe="/usr/lib/podlaz/xray",
                expected_cgroup_suffix="/podlazd.service",
            )

    def test_rejects_foreign_xray_before_baseline(self) -> None:
        cgroup_path = "/system.slice/podlazd.service"
        config_ref = "/run/podlaz/generated/xray.json"
        self.write_cgroup(cgroup_path)
        self.write_process(
            101,
            exe="/usr/bin/podlazd",
            comm="podlazd",
            cgroup_path=cgroup_path,
            start_time=1001,
            rss_kib=32000,
            pss_kib=30000,
            threads=8,
            fds=12,
            tasks=8,
        )
        self.write_process(
            202,
            exe="/usr/lib/podlaz/xray",
            comm="xray",
            cgroup_path=cgroup_path,
            start_time=2002,
            rss_kib=180000,
            pss_kib=170000,
            threads=27,
            fds=21,
            tasks=27,
            parent_pid=101,
            cmdline=("/usr/lib/podlaz/xray", "run", "-config", config_ref),
        )
        self.write_process(
            303,
            exe="/opt/foreign/xray",
            comm="xray",
            cgroup_path="/user.slice/foreign.service",
            start_time=3003,
            rss_kib=1000,
            pss_kib=900,
            threads=2,
            fds=3,
            tasks=2,
        )
        self.write_transaction(202, config_ref)

        with self.assertRaisesRegex(tun_soak_metrics.AttributionError, "foreign VPN/core"):
            tun_soak_metrics.discover_active_identity(
                daemon_pid=101,
                transaction_dir=self.transactions,
                proc_root=self.proc,
                cgroup_root=self.cgroup,
                daemon_exe="/usr/bin/podlazd",
                xray_exe="/usr/lib/podlaz/xray",
                expected_cgroup_suffix="/podlazd.service",
            )

    def test_rejects_supervised_child_with_additional_config_reference(self) -> None:
        cgroup_path = "/system.slice/podlazd.service"
        config_ref = "/run/podlaz/generated/xray.json"
        self.write_cgroup(cgroup_path)
        self.write_process(
            101,
            exe="/usr/bin/podlazd",
            comm="podlazd",
            cgroup_path=cgroup_path,
            start_time=1001,
            rss_kib=32000,
            pss_kib=30000,
            threads=8,
            fds=12,
            tasks=8,
        )
        self.write_process(
            202,
            exe="/usr/lib/podlaz/xray",
            comm="xray",
            cgroup_path=cgroup_path,
            start_time=2002,
            rss_kib=180000,
            pss_kib=170000,
            threads=27,
            fds=21,
            tasks=27,
            parent_pid=101,
            cmdline=(
                "/usr/lib/podlaz/xray",
                "run",
                "-config",
                config_ref,
                "-config",
                "/run/podlaz/generated/other.json",
            ),
        )
        self.write_transaction(202, config_ref)

        with self.assertRaisesRegex(tun_soak_metrics.AttributionError, "exact durable runtime config"):
            tun_soak_metrics.discover_active_identity(
                daemon_pid=101,
                transaction_dir=self.transactions,
                proc_root=self.proc,
                cgroup_root=self.cgroup,
                daemon_exe="/usr/bin/podlazd",
                xray_exe="/usr/lib/podlaz/xray",
                expected_cgroup_suffix="/podlazd.service",
            )

    def test_rejects_child_outside_service_cgroup(self) -> None:
        daemon_cgroup = "/system.slice/podlazd.service"
        config_ref = "/run/podlaz/generated/xray.json"
        self.write_cgroup(daemon_cgroup)
        self.write_process(
            101,
            exe="/usr/bin/podlazd",
            comm="podlazd",
            cgroup_path=daemon_cgroup,
            start_time=1001,
            rss_kib=32000,
            pss_kib=30000,
            threads=8,
            fds=12,
            tasks=8,
        )
        self.write_process(
            202,
            exe="/usr/lib/podlaz/xray",
            comm="xray",
            cgroup_path="/user.slice/foreign.service",
            start_time=2002,
            rss_kib=180000,
            pss_kib=170000,
            threads=27,
            fds=21,
            tasks=27,
            parent_pid=101,
            cmdline=("/usr/lib/podlaz/xray", "run", "-config", config_ref),
        )
        self.write_transaction(202, config_ref)

        with self.assertRaisesRegex(tun_soak_metrics.AttributionError, "service cgroup"):
            tun_soak_metrics.discover_active_identity(
                daemon_pid=101,
                transaction_dir=self.transactions,
                proc_root=self.proc,
                cgroup_root=self.cgroup,
                daemon_exe="/usr/bin/podlazd",
                xray_exe="/usr/lib/podlaz/xray",
                expected_cgroup_suffix="/podlazd.service",
            )

    def test_sampling_rejects_supervised_child_reparenting(self) -> None:
        cgroup_path = "/system.slice/podlazd.service"
        config_ref = "/run/podlaz/generated/xray.json"
        self.write_cgroup(cgroup_path)
        self.write_process(
            101,
            exe="/usr/bin/podlazd",
            comm="podlazd",
            cgroup_path=cgroup_path,
            start_time=1001,
            rss_kib=32000,
            pss_kib=30000,
            threads=8,
            fds=12,
            tasks=8,
        )
        self.write_process(
            202,
            exe="/usr/lib/podlaz/xray",
            comm="xray",
            cgroup_path=cgroup_path,
            start_time=2002,
            rss_kib=180000,
            pss_kib=170000,
            threads=27,
            fds=21,
            tasks=27,
            parent_pid=101,
            cmdline=("/usr/lib/podlaz/xray", "run", "-config", config_ref),
        )
        self.write_transaction(202, config_ref)
        identity = tun_soak_metrics.discover_active_identity(
            daemon_pid=101,
            transaction_dir=self.transactions,
            proc_root=self.proc,
            cgroup_root=self.cgroup,
            daemon_exe="/usr/bin/podlazd",
            xray_exe="/usr/lib/podlaz/xray",
            expected_cgroup_suffix="/podlazd.service",
        )

        self.rewrite_parent_pid(202, 1)

        with self.assertRaisesRegex(tun_soak_metrics.AttributionError, "parent identity changed"):
            tun_soak_metrics.collect_sample(
                identity,
                proc_root=self.proc,
                cgroup_root=self.cgroup,
                phase="active",
                session=1,
                sample_index=0,
                elapsed_seconds=0,
            )

    def test_summarizes_component_specific_sustained_growth_without_using_peak_as_a_signal(self) -> None:
        samples = []
        for index in range(8):
            elapsed = index * 600
            samples.append(
                {
                    "schema_version": 1,
                    "phase": "active",
                    "session": 1,
                    "sample_index": index,
                    "elapsed_seconds": elapsed,
                    "cgroup": {
                        "memory_current_bytes": 220_000_000 + index * 34_000_000,
                        "memory_peak_bytes": 900_000_000 + index * 100_000_000,
                        "pids_current": 35,
                        "cpu_usage_usec": index * 10_000,
                    },
                    "podlazd": {
                        "rss_bytes": 45_000_000,
                        "pss_bytes": 41_000_000,
                        "threads": 12,
                        "tasks": 12,
                        "fds": 18,
                        "cpu_time_ticks": index * 5,
                    },
                    "xray": {
                        "rss_bytes": 170_000_000 + index * 34_000_000,
                        "pss_bytes": 160_000_000 + index * 34_000_000,
                        "threads": 23,
                        "tasks": 23,
                        "fds": 20,
                        "cpu_time_ticks": index * 100,
                    },
                }
            )

        summary = tun_soak_metrics.summarize_samples(samples)

        self.assertEqual("xray.pss_bytes", summary["reproduced_growth_candidate"])
        self.assertTrue(summary["metrics"]["xray.pss_bytes"]["sustained_positive"])
        self.assertFalse(summary["metrics"]["podlazd.pss_bytes"]["sustained_positive"])
        self.assertNotIn("cgroup.memory_peak_bytes", summary["growth_candidates"])
        self.assertEqual("historical_high_water_mark", summary["metrics"]["cgroup.memory_peak_bytes"]["semantics"])


    def test_reconnect_requires_a_new_supervised_child_identity(self) -> None:
        daemon = tun_soak_metrics.ProcessIdentity(
            pid=101, parent_pid=1, start_time_ticks=1001, exe="/usr/bin/podlazd", cgroup_path="/system.slice/podlazd.service"
        )
        first = tun_soak_metrics.ActiveIdentity(
            daemon=daemon,
            xray=tun_soak_metrics.ProcessIdentity(
                pid=202, parent_pid=101, start_time_ticks=2002, exe="/usr/lib/podlaz/xray", cgroup_path="/system.slice/podlazd.service"
            ),
            transaction_file="/run/podlaz/transactions/private-a.json",
            config_ref="/run/podlaz/generated/xray.json",
        )
        replacement = tun_soak_metrics.ActiveIdentity(
            daemon=daemon,
            xray=tun_soak_metrics.ProcessIdentity(
                pid=303, parent_pid=101, start_time_ticks=3003, exe="/usr/lib/podlaz/xray", cgroup_path="/system.slice/podlazd.service"
            ),
            transaction_file="/run/podlaz/transactions/private-b.json",
            config_ref="/run/podlaz/generated/xray.json",
        )

        tun_soak_metrics.assert_replaced(first, replacement)
        with self.assertRaisesRegex(tun_soak_metrics.AttributionError, "not replaced"):
            tun_soak_metrics.assert_replaced(first, first)

    def test_acceptance_policy_is_metric_specific_and_rejects_unruled_growth(self) -> None:
        samples = []
        for index in range(8):
            samples.append(
                {
                    "schema_version": 1,
                    "phase": "active",
                    "session": 1,
                    "sample_index": index,
                    "elapsed_seconds": index * 600,
                    "cgroup": {
                        "memory_current_bytes": 210_000_000 + index * 20_000_000,
                        "memory_peak_bytes": 400_000_000,
                        "pids_current": 32,
                        "cpu_usage_usec": index * 10_000,
                    },
                    "podlazd": {
                        "rss_bytes": 40_000_000,
                        "pss_bytes": 36_000_000,
                        "threads": 10,
                        "tasks": 10,
                        "fds": 16,
                        "cpu_time_ticks": index,
                    },
                    "xray": {
                        "rss_bytes": 170_000_000 + index * 20_000_000,
                        "pss_bytes": 160_000_000 + index * 20_000_000,
                        "threads": 22,
                        "tasks": 22,
                        "fds": 20,
                        "cpu_time_ticks": index * 10,
                    },
                }
            )
        trend = tun_soak_metrics.summarize_samples(samples)
        policy = {
            "schema_version": 1,
            "mode": "accept",
            "reproduced_growth_signal": "xray.pss_bytes",
            "metric_limits": {
                "xray.pss_bytes": {
                    "max_theil_sen_per_hour": 130_000_000,
                    "max_net_growth": 150_000_000,
                }
            },
        }
        result = tun_soak_metrics.evaluate_policy(trend, policy)
        self.assertFalse(result["ok"])
        self.assertIn("xray.pss_bytes", result["violations"])
        self.assertIn("xray.rss_bytes", result["violations"])

    def test_cleanup_comparison_is_strict_for_counts_but_tolerant_for_memory(self) -> None:
        result = tun_soak_metrics.compare_cleanup_boundaries(
            baseline={
                "podlazd": {"rss_bytes": 40_000_000, "pss_bytes": 36_000_000, "threads": 10, "tasks": 10, "fds": 16},
                "cgroup": {"memory_current_bytes": 42_000_000, "pids_current": 10},
            },
            cleanup={
                "podlazd": {"rss_bytes": 44_000_000, "pss_bytes": 39_000_000, "threads": 10, "tasks": 10, "fds": 16},
                "cgroup": {"memory_current_bytes": 46_000_000, "pids_current": 10},
            },
            memory_tolerance_bytes=8 * 1024 * 1024,
        )
        self.assertTrue(result["ok"])

        retained = tun_soak_metrics.compare_cleanup_boundaries(
            baseline={
                "podlazd": {"rss_bytes": 40_000_000, "pss_bytes": 36_000_000, "threads": 10, "tasks": 10, "fds": 16},
                "cgroup": {"memory_current_bytes": 42_000_000, "pids_current": 10},
            },
            cleanup={
                "podlazd": {"rss_bytes": 44_000_000, "pss_bytes": 39_000_000, "threads": 11, "tasks": 11, "fds": 17},
                "cgroup": {"memory_current_bytes": 46_000_000, "pids_current": 11},
            },
            memory_tolerance_bytes=8 * 1024 * 1024,
        )
        self.assertFalse(retained["ok"])
        self.assertIn("podlazd.fds", retained["violations"])
        self.assertIn("podlazd.tasks", retained["violations"])


    def test_build_report_combines_trend_policy_and_lifecycle_without_private_values(self) -> None:
        active_samples = []
        reconnect_samples = []
        for index in range(8):
            active_samples.append(
                {
                    "schema_version": 1,
                    "phase": "active",
                    "session": 1,
                    "sample_index": index,
                    "elapsed_seconds": index * 600,
                    "cgroup": {
                        "memory_current_bytes": 210_000_000,
                        "memory_peak_bytes": 260_000_000,
                        "pids_current": 32,
                        "cpu_usage_usec": index * 10_000,
                    },
                    "podlazd": {
                        "rss_bytes": 40_000_000,
                        "pss_bytes": 36_000_000,
                        "threads": 10,
                        "tasks": 10,
                        "fds": 16,
                        "cpu_time_ticks": index,
                    },
                    "xray": {
                        "rss_bytes": 170_000_000,
                        "pss_bytes": 160_000_000,
                        "threads": 22,
                        "tasks": 22,
                        "fds": 20,
                        "cpu_time_ticks": index * 10,
                    },
                }
            )
        reconnect_samples.append(
            {
                **active_samples[0],
                "phase": "reconnect",
                "session": 2,
                "sample_index": 0,
            }
        )
        baseline = {
            "schema_version": 1,
            "phase": "inactive-baseline",
            "session": 0,
            "sample_index": 0,
            "elapsed_seconds": 0,
            "cgroup": {
                "memory_current_bytes": 42_000_000,
                "memory_peak_bytes": 42_000_000,
                "pids_current": 10,
                "cpu_usage_usec": 0,
            },
            "podlazd": {
                "rss_bytes": 40_000_000,
                "pss_bytes": 36_000_000,
                "threads": 10,
                "tasks": 10,
                "fds": 16,
                "cpu_time_ticks": 0,
            },
            "xray": None,
        }
        cleanup = {
            **baseline,
            "phase": "post-cleanup",
            "cgroup": {
                **baseline["cgroup"],
                "memory_current_bytes": 45_000_000,
                "pids_current": 12,
            },
            "podlazd": {
                **baseline["podlazd"],
                "rss_bytes": 43_000_000,
                "threads": 12,
                "tasks": 12,
            },
        }
        reconnect_cleanup = {
            **cleanup,
            "sample_index": 1,
            "cgroup": {**cleanup["cgroup"], "memory_current_bytes": 46_000_000},
            "podlazd": {**cleanup["podlazd"], "rss_bytes": 44_000_000},
        }

        report = tun_soak_metrics.build_report(
            active_samples=active_samples,
            reconnect_samples=reconnect_samples,
            baseline_boundary=baseline,
            cleanup_boundary=cleanup,
            reconnect_cleanup_boundary=reconnect_cleanup,
            provenance={
                "podlaz_version": "0.2.26",
                "podlaz_commit": "0123456789abcdef",
                "xray_version": "v26.3.27",
                "xray_artifact_sha256": "b" * 64,
                "xray_binary_sha256": "c" * 64,
                "kernel_release": "6.8.0-test-generic",
                "systemd_version": "255",
                "package_sha256": "a" * 64,
                "package_architecture": "amd64",
            },
            configuration={
                "duration_seconds": 4200,
                "warmup_seconds": 120,
                "sample_interval_seconds": 600,
                "doctor_every_samples": 3,
                "doctor_runs": 2,
                "doctor_unhealthy_runs": 1,
                "reconnect_samples": 1,
                "tun_diagnostic_timeout_seconds": 90,
                "tun_health_timeout_seconds": 75,
            },
            policy={
                "schema_version": 1,
                "mode": "observe",
                "reproduced_growth_signal": None,
                "metric_limits": {},
            },
            cleanup_memory_tolerance_bytes=8 * 1024 * 1024,
            reconnect_memory_tolerance_bytes=8 * 1024 * 1024,
            reconnect_count_tolerance=0,
        )

        self.assertEqual("observation_complete", report["verdict"])
        self.assertTrue(report["lifecycle"]["cleanup"]["ok"])
        self.assertEqual("equivalent-post-cleanup", report["lifecycle"]["cleanup"]["comparison"])
        self.assertTrue(report["lifecycle"]["reconnect"]["ok"])
        self.assertIsNone(report["trend"]["reproduced_growth_candidate"])
        self.assertEqual(75, report["configuration"]["tun_health_timeout_seconds"])
        self.assertEqual(90, report["configuration"]["tun_diagnostic_timeout_seconds"])
        self.assertEqual(2, report["configuration"]["doctor_runs"])
        self.assertEqual(1, report["configuration"]["doctor_unhealthy_runs"])
        encoded = json.dumps(report, sort_keys=True)
        self.assertNotIn("private-transaction-id", encoded)
        self.assertNotIn("/run/podlaz", encoded)
        self.assertNotIn('"pid":', encoded.lower())
        self.assertNotIn("transaction_file", encoded)
        self.assertNotIn("config_ref", encoded)

    def test_public_configuration_rejects_more_unhealthy_doctor_results_than_runs(self) -> None:
        with self.assertRaisesRegex(ValueError, "doctor_unhealthy_runs"):
            tun_soak_analysis._public_configuration(
                {
                    "duration_seconds": 3600,
                    "warmup_seconds": 120,
                    "sample_interval_seconds": 60,
                    "doctor_every_samples": 10,
                    "doctor_runs": 1,
                    "doctor_unhealthy_runs": 2,
                    "reconnect_samples": 3,
                    "tun_diagnostic_timeout_seconds": 90,
                    "tun_health_timeout_seconds": 75,
                }
            )

    def test_classifies_private_cli_errors_without_returning_raw_text(self) -> None:
        cases = {
            "podlaz: authorization denied: polkit denied io.github.example.disconnect\n": "authorization-denied",
            "podlaz: authorization unavailable: polkit is unavailable\n": "authorization-unavailable",
            "podlaz: daemon disconnect request failed: unexpected HTTP status 500 Internal Server Error\n": "daemon-internal",
            "podlaz: daemon is unavailable\n": "daemon-unavailable",
            "private-host.example invalid-profile-token\n": "unclassified",
        }
        for raw, expected in cases.items():
            with self.subTest(expected=expected):
                classification = tun_soak_metrics.classify_cli_failure(raw)
                self.assertEqual(expected, classification)
                self.assertNotIn("example", classification)
                self.assertNotIn("token", classification)

    def test_classify_cli_error_command_emits_only_allowlisted_value(self) -> None:
        stderr_file = self.root / "private.stderr"
        stderr_file.write_text(
            "podlaz: authorization unavailable: polkit is unavailable for disconnect\n",
            encoding="utf-8",
        )
        parser = tun_soak_metrics.build_parser()
        args = parser.parse_args(["classify-cli-error", "--stderr-file", str(stderr_file)])
        self.assertEqual("classify-cli-error", args.command)
        self.assertEqual("authorization-unavailable", tun_soak_metrics.classify_cli_failure(stderr_file.read_text()))

    def test_build_report_rejects_unknown_public_metadata_fields(self) -> None:
        sample = {
            "schema_version": 1,
            "phase": "active",
            "session": 1,
            "sample_index": 0,
            "elapsed_seconds": 0,
            "cgroup": {"memory_current_bytes": 1, "memory_peak_bytes": 1, "pids_current": 1, "cpu_usage_usec": 1},
            "podlazd": {"rss_bytes": 1, "pss_bytes": 1, "threads": 1, "tasks": 1, "fds": 1, "cpu_time_ticks": 1},
            "xray": {"rss_bytes": 1, "pss_bytes": 1, "threads": 1, "tasks": 1, "fds": 1, "cpu_time_ticks": 1},
        }
        baseline = {
            **sample,
            "phase": "inactive-baseline",
            "session": 0,
            "xray": None,
        }
        reconnect = {**sample, "phase": "reconnect", "session": 2}
        with self.assertRaisesRegex(ValueError, "unsupported provenance field"):
            tun_soak_metrics.build_report(
                active_samples=[sample] * 6,
                reconnect_samples=[reconnect],
                baseline_boundary=baseline,
                cleanup_boundary={**baseline, "phase": "post-cleanup"},
                reconnect_cleanup_boundary={**baseline, "phase": "post-cleanup", "sample_index": 1},
                provenance={"podlaz_version": "0.2.26", "profile_id": "secret"},
                configuration={"duration_seconds": 3600},
                policy={"mode": "observe"},
                cleanup_memory_tolerance_bytes=0,
                reconnect_memory_tolerance_bytes=0,
                reconnect_count_tolerance=0,
            )


if __name__ == "__main__":
    unittest.main()
