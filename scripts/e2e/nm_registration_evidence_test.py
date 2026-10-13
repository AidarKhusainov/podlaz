#!/usr/bin/env python3
"""Unit characterization of guest-private NM registration snapshot normalization."""
import unittest

from nm_registration_evidence import strip_runtime_counters


class SnapshotNormalizationTest(unittest.TestCase):
    def test_packet_counters_are_not_authority(self):
        original = {"nftables": [{"rule": {"expr": [
            {"counter": {"packets": 10, "bytes": 200}},
            {"accept": None},
        ]}}]}
        updated = {"nftables": [{"rule": {"expr": [
            {"counter": {"packets": 11, "bytes": 230}},
            {"accept": None},
        ]}}]}
        self.assertEqual(strip_runtime_counters(original), strip_runtime_counters(updated))

    def test_rule_identity_and_payload_are_not_ignored(self):
        old = {"nftables": [{"rule": {"expr": [{"drop": None}, {"counter": {"packets": 5}}]}}]}
        changed = {"nftables": [{"rule": {"expr": [{"accept": None}, {"counter": {"packets": 5}}]}}]}
        self.assertNotEqual(strip_runtime_counters(old), strip_runtime_counters(changed))

    def test_nested_unrelated_values_are_preserved(self):
        value = {"routes": [{"table": 123, "dst": "default"}], "nftables": [
            {"rule": {"counter": {"packets": 12, "bytes": 500, "other": "owned"}}}]}
        result = strip_runtime_counters(value)
        self.assertEqual(result["routes"], value["routes"])
        self.assertEqual(result["nftables"][0]["rule"]["counter"], {"other": "owned"})


if __name__ == "__main__":
    unittest.main()
