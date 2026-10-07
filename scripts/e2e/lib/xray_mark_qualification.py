#!/usr/bin/env python3
"""Test-scoped native Xray sockopt mark composition for qualification only."""

from __future__ import annotations

import copy
import json
import sys
from typing import Any


class MarkConflict(ValueError):
    pass


def compose_outbound_mark(outbound: dict[str, Any], mark: int) -> dict[str, Any]:
    if mark <= 0:
        raise ValueError("qualification mark must be non-zero")

    result = copy.deepcopy(outbound)
    stream = result.setdefault("streamSettings", {})
    if not isinstance(stream, dict):
        raise ValueError("streamSettings must be an object")
    sockopt = stream.setdefault("sockopt", {})
    if not isinstance(sockopt, dict):
        raise ValueError("streamSettings.sockopt must be an object")

    provider_mark = sockopt.get("mark", 0)
    if isinstance(provider_mark, bool) or not isinstance(provider_mark, int) or provider_mark < 0:
        raise ValueError("provider sockopt.mark must be a non-negative integer")
    if provider_mark not in (0, mark):
        raise MarkConflict(
            f"provider sockopt.mark {provider_mark} conflicts with qualification mark {mark}"
        )

    sockopt["mark"] = mark
    return result


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: xray_mark_qualification.py <mark> <outbound-json>", file=sys.stderr)
        return 2
    mark = int(sys.argv[1], 0)
    outbound = json.loads(sys.argv[2])
    try:
        result = compose_outbound_mark(outbound, mark)
    except MarkConflict as exc:
        print(str(exc), file=sys.stderr)
        return 3
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
