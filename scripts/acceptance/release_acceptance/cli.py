from __future__ import annotations

import argparse
from pathlib import Path
import pwd
import sys

from .model import AcceptanceError, PreflightError, RunConfig, RunMode, UserIdentity
from .orchestrator import ReleaseAcceptance


def parse_args(argv: list[str]) -> RunConfig:
    parser = argparse.ArgumentParser(
        prog="release-laptop.sh",
        description="Qualify an already-built Podlaz Debian release on a maintainer laptop.",
    )
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument(
        "--resume",
        action="store_true",
        help="resume a supported persisted package-setup or post-reboot phase",
    )
    mode.add_argument(
        "--abort",
        action="store_true",
        help="abandon the current run and restore exact harness-owned state",
    )
    parser.add_argument("candidate", nargs="?", type=Path, help="candidate Podlaz .deb")
    parser.add_argument("--previous-deb", type=Path, help="explicit strictly-lower Podlaz release .deb")
    parser.add_argument("--profile", help="existing user profile id")
    parser.add_argument("--artifact-dir", type=Path, help="private/public evidence root")
    parser.add_argument("--soak-minutes", type=int, default=60, metavar="N", help="active soak duration; values below 60 cap result at PARTIAL_PASS")
    parser.add_argument("--skip-wifi-reconnect", action="store_true", help="skip controlled NetworkManager reconnect; caps result at PARTIAL_PASS")
    parser.add_argument("--skip-suspend", action="store_true", help="skip timed suspend/resume; caps result at PARTIAL_PASS")
    parser.add_argument("--no-reboot-phases", action="store_true", help="skip the three real reboot phases; caps result at PARTIAL_PASS")
    args = parser.parse_args(argv)

    if args.resume or args.abort:
        illegal = any((args.candidate, args.previous_deb, args.profile, args.artifact_dir)) or args.soak_minutes != 60 or args.skip_wifi_reconnect or args.skip_suspend or args.no_reboot_phases
        if illegal:
            parser.error("--resume/--abort do not accept new-run inputs")
        return RunConfig(mode=RunMode.RESUME if args.resume else RunMode.ABORT)

    if args.candidate is None:
        parser.error("candidate .deb is required for a new run")
    if args.soak_minutes <= 0 or args.soak_minutes > 24 * 60:
        parser.error("--soak-minutes must be between 1 and 1440")
    return RunConfig(
        mode=RunMode.NEW,
        candidate=args.candidate,
        previous_deb=args.previous_deb,
        profile=args.profile,
        artifact_dir=args.artifact_dir,
        soak_minutes=args.soak_minutes,
        allow_wifi_reconnect=not args.skip_wifi_reconnect,
        allow_suspend=not args.skip_suspend,
        reboot_phases=not args.no_reboot_phases,
    )


def resolve_original_user() -> UserIdentity:
    import os

    name = (os.environ.get("SUDO_USER") or "").strip()
    if not name or name == "root":
        raise PreflightError("SUDO_USER must identify the original non-root user")
    try:
        record = pwd.getpwnam(name)
    except KeyError as error:
        raise PreflightError(f"SUDO_USER does not resolve through the account database: {name}") from error
    if record.pw_uid == 0:
        raise PreflightError("original user must not be root")
    return UserIdentity(name, record.pw_uid, record.pw_gid, Path(record.pw_dir))


def checkpoint_path(user: UserIdentity) -> Path:
    return user.state_home / "podlaz" / "release-acceptance" / "current.json"


def main(argv: list[str] | None = None) -> int:
    config = parse_args(list(sys.argv[1:] if argv is None else argv))
    try:
        user = resolve_original_user()
        harness = ReleaseAcceptance(user, checkpoint_path(user))
        if config.mode == RunMode.NEW:
            result = harness.run_new(config)
            if result is None:
                print("Release acceptance checkpoint saved.")
                print("Reboot the laptop, then run: sudo ./scripts/acceptance/release-laptop.sh --resume")
                return 0
            print(result.value)
            return 1 if result.value == "FAIL" else 0
        if config.mode == RunMode.RESUME:
            result = harness.resume()
            if result is None:
                print("Release acceptance checkpoint advanced.")
                print("Reboot the laptop again, then run: sudo ./scripts/acceptance/release-laptop.sh --resume")
                return 0
            print(result.value)
            return 1 if result.value == "FAIL" else 0
        result = harness.abort()
        print(result)
        return 0 if result == "ABORTED_CLEAN" else 1
    except PreflightError as error:
        print(f"release-laptop: {error}", file=sys.stderr)
        return 2
    except (AcceptanceError, OSError, ValueError) as error:
        print(f"release-laptop: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
