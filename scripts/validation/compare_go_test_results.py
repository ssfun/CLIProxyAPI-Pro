#!/usr/bin/env python3
"""Compare Go test failures before and after applying a customization."""

from __future__ import annotations

import argparse
import json
from collections import defaultdict
from pathlib import Path


Failure = tuple[str, str]


def read_failures(path: Path) -> tuple[set[Failure], dict[Failure, list[str]]]:
    failures: set[Failure] = set()
    output: dict[Failure, list[str]] = defaultdict(list)

    for raw_line in path.read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            event = json.loads(raw_line)
        except json.JSONDecodeError:
            continue

        if event.get("Action") == "output" and event.get("Output"):
            package = str(event.get("Package", ""))
            test = str(event.get("Test", ""))
            key = (package, test)
            if len(output[key]) < 24:
                output[key].append(str(event["Output"]).rstrip())
            continue

        if event.get("Action") != "fail":
            continue

        package = str(event.get("Package", ""))
        test = str(event.get("Test", ""))
        key = (package, test)
        failures.add(key)

    return failures, {key: lines for key, lines in output.items() if key in failures}


def format_failure(key: Failure) -> str:
    package, test = key
    return f"{package}::{test}" if test else package


def compare(baseline_path: Path, candidate_path: Path) -> int:
    baseline, _ = read_failures(baseline_path)
    candidate, candidate_output = read_failures(candidate_path)
    new_failures = sorted(candidate - baseline)

    print(
        f"Go test baseline comparison: baseline failures={len(baseline)}, "
        f"candidate failures={len(candidate)}, new failures={len(new_failures)}"
    )
    if not new_failures:
        if baseline:
            print("OK: candidate contains only failures already present in clean upstream")
        return 0

    print("New failures introduced after applying CLIProxyAPI Pro customizations:")
    for failure in new_failures:
        print(f"  - {format_failure(failure)}")
        for line in candidate_output.get(failure, []):
            if line:
                print(f"      {line}")
    return 1


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("baseline", type=Path)
    parser.add_argument("candidate", type=Path)
    args = parser.parse_args()
    return compare(args.baseline, args.candidate)


if __name__ == "__main__":
    raise SystemExit(main())
