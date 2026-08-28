from __future__ import annotations

from dataclasses import asdict, dataclass
import time

from .fixtures import FixtureLease, FIXTURE_B
from .host_events import SuspendEvent, WifiLease
from .lifecycle import LifecycleScenarios
from .model import ScenarioOutcome
from .privacy import PrivacyObserver
from .product import ProductClient
from .resources import ResourceSampler, ResourceSummary


@dataclass(frozen=True)
class SoakResult:
    measured_seconds: int
    requested_minutes: int
    events: tuple[str, ...]
    wifi_outcome: str
    suspend_outcome: str
    resource_summary: dict
    forces_partial: bool


class SoakScheduler:
    def __init__(
        self,
        *,
        product: ProductClient,
        lifecycle: LifecycleScenarios,
        privacy: PrivacyObserver,
        sampler: ResourceSampler,
        fixture_b: FixtureLease,
        wifi: WifiLease,
        suspend: SuspendEvent,
    ):
        self.product = product
        self.lifecycle = lifecycle
        self.privacy = privacy
        self.sampler = sampler
        self.fixture_b = fixture_b
        self.wifi = wifi
        self.suspend = suspend

    def run(self, minutes: int, *, allow_wifi: bool, allow_suspend: bool) -> SoakResult:
        started = time.monotonic()
        deadline = started + minutes * 60
        next_sample = started
        next_doctor = started + 10 * 60
        fixture_at = started + 20 * 60
        wifi_at = started + 30 * 60
        suspend_at = started + 40 * 60
        fixture_done = wifi_done = suspend_done = False
        events: list[str] = []
        samples = []
        wifi_outcome = ScenarioOutcome.NOT_EXERCISED.value
        suspend_outcome = ScenarioOutcome.NOT_EXERCISED.value

        while time.monotonic() < deadline:
            now = time.monotonic()
            if now >= next_sample:
                samples.append(self.sampler.sample())
                verdict = self.privacy.observe_protected()
                if verdict.outcome != ScenarioOutcome.PASS:
                    raise RuntimeError(f"privacy failed during soak: {verdict.reason}")
                next_sample += 60
            if now >= next_doctor:
                result = self.product.doctor_tun()
                if result.returncode not in {0, 3}:
                    raise RuntimeError(f"doctor --tun failed during soak with exit {result.returncode}")
                next_doctor += 10 * 60
            if not fixture_done and now >= fixture_at:
                self.fixture_b.acquire()
                self.fixture_b.churn()
                self.fixture_b.verify()
                events.append("fixture_b@20")
                fixture_done = True
            if not wifi_done and now >= wifi_at:
                if allow_wifi:
                    result = self.wifi.reconnect()
                    wifi_outcome = result.outcome.value
                    if result.outcome == ScenarioOutcome.PASS:
                        self.lifecycle.wait_verified_active()
                        verdict = self.privacy.observe_protected()
                        if verdict.outcome != ScenarioOutcome.PASS:
                            raise RuntimeError(verdict.reason)
                else:
                    wifi_outcome = ScenarioOutcome.SKIP_USER_REQUEST.value
                events.append("wifi@30")
                wifi_done = True
            if not suspend_done and now >= suspend_at:
                if allow_suspend:
                    result = self.suspend.run()
                    suspend_outcome = result.outcome.value
                    if result.outcome == ScenarioOutcome.PASS:
                        self.lifecycle.wait_verified_active()
                        verdict = self.privacy.observe_protected()
                        if verdict.outcome != ScenarioOutcome.PASS:
                            raise RuntimeError(verdict.reason)
                else:
                    suspend_outcome = ScenarioOutcome.SKIP_USER_REQUEST.value
                events.append("suspend@40")
                suspend_done = True
            time.sleep(min(1.0, max(0.05, deadline - time.monotonic())))

        if fixture_done:
            self.fixture_b.release()
        summary = ResourceSampler.summarize(samples)
        measured = int(time.monotonic() - started)
        return SoakResult(
            measured_seconds=measured,
            requested_minutes=minutes,
            events=tuple(events),
            wifi_outcome=wifi_outcome,
            suspend_outcome=suspend_outcome,
            resource_summary=asdict(summary),
            forces_partial=minutes < 60 or wifi_outcome == ScenarioOutcome.SKIP_USER_REQUEST.value or suspend_outcome == ScenarioOutcome.SKIP_USER_REQUEST.value,
        )
