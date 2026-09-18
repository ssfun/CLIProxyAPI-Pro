#!/usr/bin/env python3
"""Classify repository and upstream changes for the Validate workflow."""

from __future__ import annotations

import argparse
import json
from dataclasses import asdict, dataclass
from pathlib import Path


@dataclass
class Decision:
    core: bool = False
    management: bool = False
    source_image: bool = False
    reason: str = "scoped repository changes"


def is_documentation(path: str) -> bool:
    name = Path(path).name
    return (
        path == "docs"
        or path.startswith("docs/")
        or name.startswith("README")
        or name.startswith("LICENSE")
        or path.endswith(".md")
    )


def is_core_change(path: str) -> bool:
    return path.startswith("cliproxyapi-pro-core/") or path in {
        "scripts/validation/core.sh",
        "scripts/validation/compare_go_test_results.py",
        "scripts/validation/summarize_core_validation.py",
        "scripts/validation/test_core_validation_summary.py",
        "scripts/validation/test_go_test_baseline.py",
        "scripts/validation/contracts/core-upstream-modified-files.txt",
        "scripts/validation/contracts/core-upstream-modified-files.ownership.tsv",
        "scripts/validation/test_core_module_boundaries.py",
        "scripts/validation/test_core_patch_ownership.py",
    }


def is_management_change(path: str) -> bool:
    return path.startswith("cliproxyapi-pro-management/") or path in {
        "scripts/validation/management.sh",
        "scripts/validation/contracts/management-upstream-modified-files.txt",
    }


def classify(
    paths: list[str],
    *,
    force_all: bool = False,
    core_upstream_changed: bool = False,
    management_upstream_changed: bool = False,
    image_input_changed: bool = False,
) -> Decision:
    if force_all:
        return Decision(True, True, True, "no trusted prior validation state")

    decision = Decision()
    unknown_paths: list[str] = []
    for raw_path in paths:
        path = raw_path.strip()
        if not path or is_documentation(path):
            continue
        if is_core_change(path):
            decision.core = True
            decision.source_image = True
        elif is_management_change(path):
            decision.management = True
        else:
            unknown_paths.append(path)

    if unknown_paths:
        return Decision(
            True,
            True,
            True,
            f"conservative full validation for {unknown_paths[0]}",
        )

    if core_upstream_changed:
        decision.core = True
        decision.source_image = True
    if management_upstream_changed:
        decision.management = True
    if image_input_changed:
        decision.source_image = True

    if not paths and not any(
        (core_upstream_changed, management_upstream_changed, image_input_changed)
    ):
        decision.reason = "no unvalidated repository or upstream changes"
    elif all(is_documentation(path.strip()) for path in paths if path.strip()):
        decision.reason = "documentation-only repository changes"
    return decision


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--paths-file", type=Path, required=True)
    parser.add_argument("--force-all", action="store_true")
    parser.add_argument("--core-upstream-changed", action="store_true")
    parser.add_argument("--management-upstream-changed", action="store_true")
    parser.add_argument("--image-input-changed", action="store_true")
    args = parser.parse_args()
    paths = args.paths_file.read_text(encoding="utf-8").splitlines()
    decision = classify(
        paths,
        force_all=args.force_all,
        core_upstream_changed=args.core_upstream_changed,
        management_upstream_changed=args.management_upstream_changed,
        image_input_changed=args.image_input_changed,
    )
    print(json.dumps(asdict(decision), sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
