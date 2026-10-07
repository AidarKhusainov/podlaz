#!/usr/bin/env python3
import argparse
import json
from pathlib import Path


class MarkConflict(ValueError):
    pass


def apply_mark(document: dict, mark: int) -> dict:
    if not isinstance(document, dict):
        raise ValueError("native Xray source must be a JSON object")
    outbounds = document.get("outbounds")
    if not isinstance(outbounds, list) or not outbounds:
        raise ValueError("native Xray source requires a non-empty outbounds array")
    if mark <= 0:
        raise ValueError("qualification mark must be positive")

    for index, outbound in enumerate(outbounds, start=1):
        if not isinstance(outbound, dict):
            raise ValueError(f"outbound {index} must be an object")
        stream = outbound.setdefault("streamSettings", {})
        if not isinstance(stream, dict):
            raise ValueError(f"outbound {index} streamSettings must be an object")
        sockopt = stream.setdefault("sockopt", {})
        if not isinstance(sockopt, dict):
            raise ValueError(f"outbound {index} sockopt must be an object")
        provider_mark = sockopt.get("mark", 0)
        if provider_mark not in (None, 0, mark):
            raise MarkConflict(
                f"outbound {index} provider sockopt.mark {provider_mark!r} conflicts with qualification mark"
            )
        sockopt["mark"] = mark
    return document


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("input")
    parser.add_argument("output")
    parser.add_argument("--mark", type=int, required=True)
    args = parser.parse_args()

    source = Path(args.input)
    target = Path(args.output)
    document = json.loads(source.read_text(encoding="utf-8"))
    try:
        rendered = apply_mark(document, args.mark)
    except MarkConflict as exc:
        print(str(exc))
        return 3
    target.write_text(
        json.dumps(rendered, sort_keys=True, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
