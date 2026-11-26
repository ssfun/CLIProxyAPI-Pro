#!/usr/bin/env python3
"""Verify that customizations modify exactly the reviewed upstream file set."""

from __future__ import annotations

import argparse
import subprocess
from pathlib import Path, PurePosixPath


def load_manifest(path: Path) -> list[str]:
    entries = [
        line.strip()
        for line in path.read_text(encoding="utf-8").splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    ]
    if len(entries) != len(set(entries)):
        raise ValueError(f"duplicate path in patch surface manifest: {path}")
    if entries != sorted(entries):
        raise ValueError(f"patch surface manifest must be sorted: {path}")
    for entry in entries:
        normalized = PurePosixPath(entry)
        if normalized.is_absolute() or ".." in normalized.parts or str(normalized) != entry:
            raise ValueError(f"invalid patch surface path in {path}: {entry}")
    return entries


def modified_paths(root: Path) -> set[str]:
    result = subprocess.run(
        ["git", "-C", str(root), "diff", "--name-only", "--diff-filter=M", "--"],
        check=True,
        capture_output=True,
        text=True,
    )
    return {line.strip() for line in result.stdout.splitlines() if line.strip()}


def verify_surface(root: Path, manifest: Path, ignored: set[str], allow_unmodified: set[str] | None = None) -> None:
    allow_unmodified = allow_unmodified or set()
    expected = set(load_manifest(manifest))
    actual = modified_paths(root) - ignored
    missing = sorted((expected - allow_unmodified) - actual)
    unexpected = sorted(actual - expected)
    if not missing and not unexpected:
        print(f"OK: patch surface matches {manifest} ({len(expected)} modified upstream files)")
        return

    details = [f"patch surface mismatch for {root}"]
    if unexpected:
        details.append("unexpected modified upstream files:\n  " + "\n  ".join(unexpected))
    if missing:
        details.append("expected upstream files not modified:\n  " + "\n  ".join(missing))
    raise SystemExit("\n".join(details))


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=Path)
    parser.add_argument("manifest", type=Path)
    parser.add_argument("--ignore", action="append", default=[])
    parser.add_argument("--allow-unmodified", action="append", default=[])
    args = parser.parse_args()
    verify_surface(args.root.resolve(), args.manifest.resolve(), set(args.ignore), set(args.allow_unmodified))


if __name__ == "__main__":
    main()
