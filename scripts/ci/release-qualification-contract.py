#!/usr/bin/env python3
"""Guard release publication and exact-artifact acceptance against skipped gates."""

from pathlib import Path
import re

RECOVERY = (
    "daemon-recovery", "xray-replacement", "network-apply-rollback",
    "network-verify-rollback", "terminal-failure-convergence",
    "network-reconciliation", "stale-observation", "foreign-state-safety",
    "protected-gateway-lifecycle", "precommit-interruption",
)
PACKAGE = ("release-maintained-upgrade", "release-pinned-v029", "release-package-restart")
LIFECYCLE = (
    "terminal-boot", "wifi-lifecycle", "boot-authority", "suspend-resume",
    "reboot-autostart-disabled", "boot-continuation-ordering",
)
REMNAWAVE = ("release-remnawave-subscription", "release-remnawave-full-tun")
PUBLISH = (
    "resolve", "build", "installed-runtime", "synthetic-tun", "remnawave-proxy",
    *LIFECYCLE, "release-recovery-gate", "release-package-gate", *REMNAWAVE,
)


def parse_jobs(source):
    part = source.partition("\njobs:\n")
    if not part[1]:
        raise ValueError("missing jobs")
    headers = list(re.finditer(r"(?m)^  ([a-z][a-z0-9-]*):\s*$", part[2]))
    return {
        h[1]: part[2][h.start():headers[i + 1].start() if i + 1 < len(headers) else None]
        for i, h in enumerate(headers)
    }


def dependencies(job):
    found = re.search(r"(?m)^    needs:\s*(.*)$", job)
    if not found:
        return []
    if found[1]:
        return [found[1].strip()]
    result = []
    for line in job[found.end():].splitlines():
        if not line:
            continue
        item = re.fullmatch(r"      - ([a-z][a-z0-9-]*)", line)
        if not item:
            break
        result.append(item[1])
    return result


def exact_needs(jobs, job, expected):
    if job not in jobs:
        raise ValueError(f"missing {job}")
    actual = dependencies(jobs[job])
    if len(actual) != len(set(actual)) or set(actual) != set(expected):
        raise ValueError(f"{job}: incomplete or unexpected dependencies {actual}")


def validate(source):
    jobs = parse_jobs(source)
    exact_needs(jobs, "attest-and-publish", PUBLISH)
    if re.search(r"(?m)^    if:", jobs["attest-and-publish"]):
        raise ValueError("conditional publishing is not permitted")
    for aggregator, children in (
        ("release-recovery-gate", tuple("release-" + n for n in RECOVERY)),
        ("release-package-gate", PACKAGE),
    ):
        exact_needs(jobs, aggregator, children)
        text = jobs[aggregator]
        if "always()" not in text or not re.search(
            r"length == " + str(len(children)) + r"\s+and\s+all\(\.\[\];\s*\.result == .success.\)", text
        ):
            raise ValueError(f"{aggregator}: missing fail-closed result validation")

    pending = set()
    complete = set()

    def visit(name):
        if name not in jobs or name in pending:
            raise ValueError(f"missing job or dependency cycle at {name}")
        if name in complete:
            return
        pending.add(name)
        for parent in dependencies(jobs[name]):
            visit(parent)
        pending.remove(name)
        complete.add(name)

    for name in jobs:
        visit(name)

    for name in (*("release-" + n for n in RECOVERY), *PACKAGE, *LIFECYCLE, *REMNAWAVE):
        if name not in jobs:
            raise ValueError(f"missing qualification {name}")
        text = jobs[name]
        if any(s not in text for s in (
            "actions/download-artifact@", "scripts/ci/verify-release-artifacts.sh",
            "dist/release/", "PODLAZ_E2E_CANDIDATE_COMMIT",
        )):
            raise ValueError(f"{name}: missing immutable artifact handoff")
        if "bash scripts/build-deb.sh" in text:
            raise ValueError(f"{name}: rebuilding a candidate instead of qualifying release bytes")
        if not re.search(r"bash scripts/e2e/[a-z0-9-]+\.sh", text):
            raise ValueError(f"{name}: no executable acceptance")
        if not any(s in text for s in ("validate-report", "scan-report", "scan-remnawave-artifacts.sh")):
            raise ValueError(f"{name}: missing public verdict")


def negative_tests(source):
    mutations = (
        source.replace("      - release-package-gate\n", "", 1),
        source.replace("      - release-daemon-recovery\n", "      - release-recovery-gate\n", 1),
        source.replace("scripts/ci/verify-release-artifacts.sh", "true", 1),
    )
    for index, mutation in enumerate(mutations):
        if mutation == source:
            raise ValueError(f"negative fixture {index} is ineffective")
        try:
            validate(mutation)
        except ValueError:
            continue
        raise ValueError(f"unsafe release mutation {index} passed")


if __name__ == "__main__":
    workflow = Path(".github/workflows/release.yml").read_text(encoding="utf-8")
    validate(workflow)
    negative_tests(workflow)
    print("release publication graph and negative contracts: PASS")
