"""Qualify native Xray grouped topology before any privileged NM guest apply."""
import unittest

from nm_owned_tun_grouped_guest import (
    FIRST, SECOND, FIRST_TAG, SECOND_TAG, TARGET_PORT,
    native_grouped_source, verify_composed_grouped,
)


class NativeGroupedResearchTest(unittest.TestCase):
    def setUp(self):
        self.transport = {
            "outbounds": [{
                "tag": "typed",
                "protocol": "vmess",
                "settings": {"address": FIRST, "port": 443, "id": "fixture"},
                "streamSettings": {"security": "none", "sockopt": {"mark": 20570}},
            }]
        }

    def test_two_distinct_provider_outbounds_and_unmodified_routing(self):
        native = native_grouped_source(self.transport)
        self.assertEqual([o["settings"]["address"] for o in native["outbounds"]],
                         [FIRST, SECOND])
        self.assertEqual([o["tag"] for o in native["outbounds"]],
                         [FIRST_TAG, SECOND_TAG])
        self.assertEqual(native["routing"]["rules"][0]["port"], str(TARGET_PORT))
        for outbound in native["outbounds"]:
            self.assertNotIn("mark", outbound["streamSettings"].get("sockopt", {}))
            outbound["streamSettings"]["sockopt"] = {"mark": 20570}
        self.assertEqual(verify_composed_grouped(native, 20570), 443)
        self.assertEqual(self.transport["outbounds"][0]["streamSettings"]["sockopt"]["mark"], 20570)

    def test_empty_second_mark_rejected(self):
        generated = native_grouped_source(self.transport)
        generated["outbounds"][0]["streamSettings"]["sockopt"] = {"mark": 20570}
        with self.assertRaisesRegex(ValueError, "mark"):
            verify_composed_grouped(generated, 20570)

    def test_wrong_peer_or_provider_rule_fails_closed(self):
        generated = native_grouped_source(self.transport)
        for o in generated["outbounds"]:
            o["streamSettings"]["sockopt"] = {"mark": 20570}
        generated["outbounds"][1]["settings"]["address"] = FIRST
        with self.assertRaisesRegex(ValueError, "topology"):
            verify_composed_grouped(generated, 20570)
        generated["outbounds"][1]["settings"]["address"] = SECOND
        generated["routing"]["rules"][0]["outboundTag"] = FIRST_TAG
        with self.assertRaisesRegex(ValueError, "routing"):
            verify_composed_grouped(generated, 20570)

    def test_unqualified_provider_or_foreign_marks_rejected(self):
        bad = {"outbounds": [self.transport["outbounds"][0], self.transport["outbounds"][0]]}
        with self.assertRaisesRegex(ValueError, "only the qualified"):
            native_grouped_source(bad)
        bad = {"outbounds": [{**self.transport["outbounds"][0], "protocol": "freedom"}]}
        with self.assertRaisesRegex(ValueError, "protocol"):
            native_grouped_source(bad)
        bad = native_grouped_source(self.transport)
        bad["outbounds"][1]["streamSettings"]["sockopt"] = {"mark": 77}
        bad["outbounds"][0]["streamSettings"]["sockopt"] = {"mark": 20570}
        with self.assertRaisesRegex(ValueError, "mark"):
            verify_composed_grouped(bad, 20570)


if __name__ == "__main__":
    unittest.main()
