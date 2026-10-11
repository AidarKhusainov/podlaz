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
# Exact scenario entrypoints and validators are part of the release safety contract.
SCENARIOS = {
    "release-daemon-recovery": "hosted-daemon-recovery",
    "release-xray-replacement": "hosted-xray-replacement",
    "release-network-apply-rollback": "hosted-network-apply-rollback",
    "release-network-verify-rollback": "hosted-network-verify-rollback",
    "release-terminal-failure-convergence": "hosted-terminal-failure-convergence",
    "release-network-reconciliation": "hosted-network-reconciliation",
    "release-stale-observation": "hosted-stale-observation",
    "release-foreign-state-safety": "hosted-foreign-state-safety",
    "release-protected-gateway-lifecycle": "hosted-protected-gateway-lifecycle",
    "release-precommit-interruption": "hosted-precommit-interruption",
    "terminal-boot": "hosted-vm-terminal-boot",
    "wifi-lifecycle": "hosted-vm-wifi-lifecycle",
    "boot-authority": "hosted-vm-boot-authority",
    "suspend-resume": "hosted-vm-suspend-resume",
    "reboot-autostart-disabled": "hosted-vm-reboot-autostart-disabled",
    "boot-continuation-ordering": "hosted-vm-boot-continuation-ordering",
    "release-maintained-upgrade": "hosted-maintained-package-upgrade",
    "release-pinned-v029": "hosted-v029-package-recovery",
    "release-package-restart": "hosted-package-restart-recovery",
    "release-remnawave-subscription": "remnawave-subscription-acceptance",
    "release-remnawave-full-tun": "hosted-remnawave-tun",
}

PUBLISH = (
    "resolve", "build", "installed-runtime", "synthetic-tun", "remnawave-proxy",
    *LIFECYCLE, "release-recovery-gate", "release-package-gate", *REMNAWAVE,
)


def parse_jobs(source):
    part = source.partition("\njobs:\n")
    if not part[1]:
        raise ValueError("missing jobs")
    headers = list(re.finditer(r"(?m)^  ([a-z][a-z0-9-]*):[ \t]*$", part[2]))
    return {
        h[1]: part[2][h.start():headers[i + 1].start() if i + 1 < len(headers) else None]
        for i, h in enumerate(headers)
    }


def dependencies(job):
    found = re.search(r"(?m)^    needs:[ \t]*(.*)$", job)
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
    if "if: ${{ github.event_name == 'push' || github.event_name == 'workflow_dispatch' }}" not in jobs["attest-and-publish"]:
        raise ValueError("publication must be forbidden on non-publishing PR runs")
    exact_needs(jobs, "pr-release-qualification", PUBLISH)
    pr_qualification = jobs["pr-release-qualification"]
    if "github.event_name == 'pull_request' && always()" not in pr_qualification or 'length == 15 and all(.[]; .result == "success")' not in pr_qualification:
        raise ValueError("PR candidate qualification must fail closed")
    if "github.event_name != 'pull_request' && vars.PODLAZ_APT_PUBLISH_ENABLED == 'true'" not in jobs["apt-repository"]:
        raise ValueError("APT publication must not run on PR candidates")
    if "if: ${{ github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository }}" not in jobs["resolve"]:
        raise ValueError("PR qualification must accept every trusted same-repository workflow change and reject fork candidates")
    if 'tag="${GITHUB_SHA}"' not in jobs["resolve"] or 'version="0.2.47+pr.${GITHUB_SHA:0:12}"' not in jobs["resolve"]:
        raise ValueError("PR qualification must use unpublishable provenance-bound package version")
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


    for name, scenario in SCENARIOS.items():
        text = jobs[name]
        entrypoint = f"bash scripts/e2e/{scenario}.sh"
        # A validator alone is not execution: require the scenario with real arguments.
        if not any(line.strip().startswith(entrypoint + ' "') for line in text.splitlines()):
            raise ValueError(f"{name}: wrong executable acceptance invocation: {scenario}")
        if name == "release-remnawave-subscription":
            validator = "bash scripts/e2e/scan-remnawave-artifacts.sh subscription"
        elif name == "release-remnawave-full-tun":
            validator = entrypoint + " scan-report"
        else:
            validator = entrypoint + " validate-report"
        if validator not in text:
            raise ValueError(f"{name}: missing exact scenario verdict validator")
        if "EXPECTED_RELEASE_MANIFEST_SHA256:" not in text:
            raise ValueError(f"{name}: missing upstream manifest-digest authority")

    reconciliation = jobs["release-network-reconciliation"]
    for scenario in ("provider-observation", "resolved-unknown", "route-replacement", "networkmanager-uplink"):
        if f"scenario: {scenario}" not in reconciliation:
            raise ValueError(f"missing independent reconciliation variant: {scenario}")
    if "fail-fast: false" not in reconciliation:
        raise ValueError("reconciliation matrix must independently evaluate all variants")
    for value in ("MAINTAINED_RELEASE_SHA256:", "MAINTAINED_RELEASE_COMMIT:", "MAINTAINED_RELEASE_VERSION:"):
        if value not in jobs["release-maintained-upgrade"]:
            raise ValueError(f"maintained upgrade lost its historical source authority: {value}")
    if "V029_RELEASE_SHA256:" not in jobs["release-pinned-v029"]:
        raise ValueError("legacy recovery lost the pinned historical package digest")
    if "PREVIOUS_SHA256:" not in jobs["release-package-restart"]:
        raise ValueError("package restart lost the pinned historical package digest")

def negative_tests(source):
    mutations = (
        source.replace("      - release-package-gate\n", "", 1),
        source.replace("if: ${{ github.event_name == 'push' || github.event_name == 'workflow_dispatch' }}", "if: ${{ always() }}", 1),
        source.replace("      - release-daemon-recovery\n", "      - release-recovery-gate\n", 1),
        source.replace("bash scripts/e2e/hosted-daemon-recovery.sh", "bash scripts/e2e/hosted-xray-replacement.sh", 1),
        source.partition("  release-daemon-recovery:\n")[0]
        + "  release-daemon-recovery:\n"
        + source.partition("  release-daemon-recovery:\n")[2].replace(
            "scripts/ci/verify-release-artifacts.sh", "true", 1
        ),
    )
    mutations += (
        source.replace(
            "if: ${{ github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository }}",
            "if: ${{ github.event_name != 'pull_request' || true }}", 1,
        ),
        source.replace(
            "if: ${{ github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository }}",
            "if: ${{ github.event_name != 'pull_request' || (github.event.pull_request.head.repo.full_name == github.repository && github.head_ref == 'ci/release-terminal-boot-gate') }}", 1,
        ),
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
