#!/usr/bin/env python3
import argparse
import gzip
import shutil
import stat
import tarfile
import tempfile
import time
import zipfile
from pathlib import Path


ZIP_MIN_EPOCH = 315532800  # 1980-01-01T00:00:00Z


def archive_entries(root: Path, names: list[str]) -> list[tuple[Path, str]]:
    entries: list[tuple[Path, str]] = []
    seen: set[str] = set()
    for name in names:
        relative = Path(name)
        if relative.is_absolute() or '..' in relative.parts:
            raise ValueError(f'archive path must be relative and contained: {name}')
        archive_name = relative.as_posix()
        source = root / relative
        if not source.is_file():
            raise FileNotFoundError(source)
        if archive_name in seen:
            raise ValueError(f'duplicate archive path: {archive_name}')
        seen.add(archive_name)
        entries.append((source, archive_name))
    return sorted(entries, key=lambda item: item[1])


def create_tar_gz(output: Path, entries: list[tuple[Path, str]], epoch: int) -> None:
    with tempfile.TemporaryFile() as tar_buffer:
        with tarfile.open(fileobj=tar_buffer, mode='w', format=tarfile.PAX_FORMAT) as archive:
            for source, archive_name in entries:
                info = archive.gettarinfo(str(source), arcname=archive_name)
                info.uid = 0
                info.gid = 0
                info.uname = ''
                info.gname = ''
                info.mtime = epoch
                info.mode = stat.S_IMODE(source.stat().st_mode)
                with source.open('rb') as handle:
                    archive.addfile(info, handle)
        tar_buffer.seek(0)
        with output.open('wb') as raw_output:
            with gzip.GzipFile(filename='', mode='wb', fileobj=raw_output, mtime=epoch, compresslevel=9) as compressed:
                shutil.copyfileobj(tar_buffer, compressed)


def create_zip(output: Path, entries: list[tuple[Path, str]], epoch: int) -> None:
    zip_time = time.gmtime(max(epoch, ZIP_MIN_EPOCH))[:6]
    with zipfile.ZipFile(output, mode='w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for source, archive_name in entries:
            info = zipfile.ZipInfo(archive_name, date_time=zip_time)
            info.create_system = 3
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (stat.S_IFREG | stat.S_IMODE(source.stat().st_mode)) << 16
            archive.writestr(info, source.read_bytes(), compress_type=zipfile.ZIP_DEFLATED, compresslevel=9)


def create_archive(format_name: str, output: Path, root: Path, names: list[str], epoch: int) -> None:
    if epoch < 0:
        raise ValueError('source date epoch must be non-negative')
    entries = archive_entries(root, names)
    output.parent.mkdir(parents=True, exist_ok=True)
    if format_name == 'tar.gz':
        create_tar_gz(output, entries, epoch)
    elif format_name == 'zip':
        create_zip(output, entries, epoch)
    else:
        raise ValueError(f'unsupported archive format: {format_name}')


def main() -> None:
    parser = argparse.ArgumentParser(description='Create a normalized tar.gz or zip archive.')
    parser.add_argument('--format', required=True, choices=('tar.gz', 'zip'))
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--root', required=True, type=Path)
    parser.add_argument('--source-date-epoch', required=True, type=int)
    parser.add_argument('files', nargs='+')
    args = parser.parse_args()
    create_archive(args.format, args.output, args.root, args.files, args.source_date_epoch)


if __name__ == '__main__':
    main()
