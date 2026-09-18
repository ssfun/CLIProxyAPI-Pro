#!/usr/bin/env python3
"""Render Core validation timing and Go package duration summaries."""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path


def read_timings(path: Path) -> list[tuple[str, int, int]]:
    if not path.is_file():
        return []
    timings: list[tuple[str, int, int]] = []
    with path.open(encoding="utf-8", newline="") as handle:
        for row in csv.reader(handle, delimiter="\t"):
            if len(row) != 3:
                continue
            name, duration, status = row
            try:
                timings.append((name, int(duration), int(status)))
            except ValueError:
                continue
    return timings


def read_package_durations(paths: list[Path]) -> list[tuple[float, str, str]]:
    durations: dict[tuple[str, str], float] = {}
    for path in paths:
        if not path.is_file():
            continue
        label = "baseline" if "baseline" in path.name else "candidate"
        for raw_line in path.read_text(encoding="utf-8", errors="replace").splitlines():
            try:
                event = json.loads(raw_line)
            except json.JSONDecodeError:
                continue
            if event.get("Test") or event.get("Action") not in {"pass", "fail"}:
                continue
            package = str(event.get("Package", ""))
            if not package or not isinstance(event.get("Elapsed"), (int, float)):
                continue
            durations[(label, package)] = float(event["Elapsed"])
    return sorted(
        ((elapsed, label, package) for (label, package), elapsed in durations.items()),
        reverse=True,
    )


def render(timings: list[tuple[str, int, int]], packages: list[tuple[float, str, str]]) -> str:
    lines = ["## Core validation details", ""]
    if timings:
        lines.extend(["### Phase timings", "", "| Phase | Seconds | Status |", "| --- | ---: | ---: |"])
        lines.extend(
            f"| {name} | {duration} | {status} |"
            for name, duration, status in timings
        )
    else:
        lines.append("No phase timing data was produced.")

    lines.extend(["", "### Slowest Go packages", ""])
    if packages:
        lines.extend(["| Source | Package | Seconds |", "| --- | --- | ---: |"])
        lines.extend(
            f"| {label} | `{package}` | {elapsed:.2f} |"
            for elapsed, label, package in packages[:15]
        )
    else:
        lines.append("No package duration data was produced.")
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--timings", type=Path, required=True)
    parser.add_argument("--go-log", action="append", default=[], type=Path)
    args = parser.parse_args()
    print(render(read_timings(args.timings), read_package_durations(args.go_log)), end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
