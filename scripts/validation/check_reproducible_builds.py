#!/usr/bin/env python3
import json
import sys
from pathlib import Path


def require(text: str, needle: str, source: Path) -> None:
    if needle not in text:
        raise SystemExit(f'{source}: missing reproducible-build guard: {needle}')


def check_dockerfile_maintenance_policy(path: Path) -> None:
    text = path.read_text()
    for forbidden in ('DEBIAN_SNAPSHOT', 'snapshot.debian.org', 'komari-agent:latest@sha256:'):
        if forbidden in text:
            raise SystemExit(f'{path}: retired high-maintenance Docker pin remains: {forbidden}')


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit(f'Usage: {sys.argv[0]} /path/to/repository')
    root = Path(sys.argv[1]).resolve()
    compatibility = dict(
        line.split('=', 1)
        for line in (root / 'compatibility/upstream.env').read_text().splitlines()
        if line and not line.startswith('#') and '=' in line
    )
    overlay_manifest_path = root / 'cliproxyapi-pro-management/overlay-replacements.json'
    overlay_manifest = json.loads(overlay_manifest_path.read_text())
    if overlay_manifest.get('upstream', {}).get('tag') != compatibility.get('MANAGEMENT_UPSTREAM_TAG'):
        raise SystemExit(f'{overlay_manifest_path}: upstream tag must match compatibility/upstream.env')
    check_dockerfile_maintenance_policy(root / 'cliproxyapi-pro-core/Dockerfile')
    check_dockerfile_maintenance_policy(root / 'cliproxyapi-pro-core/Dockerfile.runtime')

    ci_path = root / '.github/workflows/ci.yml'
    ci = ci_path.read_text()
    require(ci, 'core_source_date_epoch:', ci_path)
    require(ci, '--build-arg "SOURCE_DATE_EPOCH=${source_date_epoch}"', ci_path)

    release_path = root / '.github/workflows/release-core.yml'
    release = release_path.read_text()
    require(release, 'source_date_epoch=${source_date_epoch}', release_path)
    require(release, 'GIT_AUTHOR_DATE="${build_date}" GIT_COMMITTER_DATE="${build_date}"', release_path)
    if release.count('create_reproducible_archive.py') != 4:
        raise SystemExit(f'{release_path}: every core archive path must use the reproducible archive helper')
    if release.count('-trimpath') < 4:
        raise SystemExit(f'{release_path}: every core build path must trim source paths')
    for forbidden in ('Compress-Archive', 'tar -C "${archive_dir}" -czf', 'BUILD_DATE="$(date'):
        if forbidden in release:
            raise SystemExit(f'{release_path}: non-reproducible release command remains: {forbidden}')


if __name__ == '__main__':
    main()
