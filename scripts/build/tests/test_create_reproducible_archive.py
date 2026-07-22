import hashlib
import importlib.util
import os
import stat
import tarfile
import tempfile
import unittest
import zipfile
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / 'create_reproducible_archive.py'
SPEC = importlib.util.spec_from_file_location('create_reproducible_archive', MODULE_PATH)
assert SPEC and SPEC.loader
ARCHIVE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ARCHIVE)


class ReproducibleArchiveTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name) / 'root'
        self.root.mkdir()
        (self.root / 'binary').write_bytes(b'binary\x00data')
        (self.root / 'README.md').write_text('documentation\n')
        (self.root / 'binary').chmod(0o755)
        (self.root / 'README.md').chmod(0o644)
        self.epoch = 1_700_000_000
        self.names = ['README.md', 'binary']

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def assert_rebuild_is_identical(self, format_name: str) -> tuple[Path, Path]:
        first = Path(self.temp_dir.name) / f'first.{format_name}'
        second = Path(self.temp_dir.name) / f'second.{format_name}'
        ARCHIVE.create_archive(format_name, first, self.root, self.names, self.epoch)
        os.utime(self.root / 'binary', (self.epoch + 500, self.epoch + 500))
        os.utime(self.root / 'README.md', (self.epoch + 900, self.epoch + 900))
        ARCHIVE.create_archive(format_name, second, self.root, list(reversed(self.names)), self.epoch)
        self.assertEqual(hashlib.sha256(first.read_bytes()).digest(), hashlib.sha256(second.read_bytes()).digest())
        return first, second

    def test_tar_gz_normalizes_order_metadata_and_gzip_header(self) -> None:
        first, _ = self.assert_rebuild_is_identical('tar.gz')
        with tarfile.open(first, 'r:gz') as archive:
            members = archive.getmembers()
        self.assertEqual(['README.md', 'binary'], [member.name for member in members])
        self.assertTrue(all(member.mtime == self.epoch and member.uid == 0 and member.gid == 0 for member in members))
        self.assertEqual(0o755, stat.S_IMODE(members[1].mode))

    def test_zip_normalizes_order_timestamp_and_permissions(self) -> None:
        first, _ = self.assert_rebuild_is_identical('zip')
        with zipfile.ZipFile(first) as archive:
            entries = archive.infolist()
        self.assertEqual(['README.md', 'binary'], [entry.filename for entry in entries])
        self.assertEqual(0o755, stat.S_IMODE(entries[1].external_attr >> 16))
        self.assertEqual(entries[0].date_time, entries[1].date_time)


if __name__ == '__main__':
    unittest.main()
