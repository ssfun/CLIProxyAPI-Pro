import hashlib
import importlib.util
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / 'apply_customizations.py'
SPEC = importlib.util.spec_from_file_location('apply_customizations', MODULE_PATH)
assert SPEC and SPEC.loader
CUSTOMIZATIONS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CUSTOMIZATIONS)


class OverlayCollisionCustomizationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.original_overlay_dir = CUSTOMIZATIONS.OVERLAY_DIR
        self.original_hashes = CUSTOMIZATIONS.OVERLAY_REPLACEMENT_HASHES

    def tearDown(self) -> None:
        CUSTOMIZATIONS.OVERLAY_DIR = self.original_overlay_dir
        CUSTOMIZATIONS.OVERLAY_REPLACEMENT_HASHES = self.original_hashes

    def test_allows_reviewed_replacement_and_idempotent_reapplication(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            overlay = root / 'overlay'
            target = root / 'target'
            relative_path = Path('src/existing.ts')
            source = overlay / relative_path
            destination = target / relative_path
            source.parent.mkdir(parents=True)
            destination.parent.mkdir(parents=True)
            source.write_text('customized\n')
            destination.write_text('upstream\n')

            upstream_hash = hashlib.sha256(destination.read_bytes()).hexdigest()
            CUSTOMIZATIONS.OVERLAY_DIR = overlay
            CUSTOMIZATIONS.OVERLAY_REPLACEMENT_HASHES = {
                relative_path.as_posix(): {upstream_hash},
            }

            CUSTOMIZATIONS.copy_overlay(target)
            self.assertEqual('customized\n', destination.read_text())
            CUSTOMIZATIONS.copy_overlay(target)
            self.assertEqual('customized\n', destination.read_text())

    def test_rejects_unreviewed_upstream_change(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            overlay = root / 'overlay'
            target = root / 'target'
            relative_path = Path('src/existing.ts')
            source = overlay / relative_path
            destination = target / relative_path
            source.parent.mkdir(parents=True)
            destination.parent.mkdir(parents=True)
            source.write_text('customized\n')
            destination.write_text('changed upstream\n')

            CUSTOMIZATIONS.OVERLAY_DIR = overlay
            CUSTOMIZATIONS.OVERLAY_REPLACEMENT_HASHES = {
                relative_path.as_posix(): {'not-the-current-hash'},
            }

            with self.assertRaisesRegex(RuntimeError, 'Upstream overlay replacement changed'):
                CUSTOMIZATIONS.copy_overlay(target)

    def test_rejects_new_overlay_collision(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            overlay = root / 'overlay'
            target = root / 'target'
            relative_path = Path('src/new-collision.ts')
            source = overlay / relative_path
            destination = target / relative_path
            source.parent.mkdir(parents=True)
            destination.parent.mkdir(parents=True)
            source.write_text('customized\n')
            destination.write_text('new upstream file\n')

            CUSTOMIZATIONS.OVERLAY_DIR = overlay
            CUSTOMIZATIONS.OVERLAY_REPLACEMENT_HASHES = {}

            with self.assertRaisesRegex(RuntimeError, 'Unexpected overlay collision'):
                CUSTOMIZATIONS.copy_overlay(target)

    def test_preflight_does_not_copy_other_files_before_rejecting(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            overlay = root / 'overlay'
            target = root / 'target'
            new_source = overlay / 'src/new.ts'
            collision_source = overlay / 'src/z-collision.ts'
            collision_target = target / 'src/z-collision.ts'
            new_source.parent.mkdir(parents=True)
            collision_target.parent.mkdir(parents=True)
            new_source.write_text('new customization\n')
            collision_source.write_text('customized collision\n')
            collision_target.write_text('upstream collision\n')

            CUSTOMIZATIONS.OVERLAY_DIR = overlay
            CUSTOMIZATIONS.OVERLAY_REPLACEMENT_HASHES = {}

            with self.assertRaisesRegex(RuntimeError, 'Unexpected overlay collision'):
                CUSTOMIZATIONS.copy_overlay(target)
            self.assertFalse((target / 'src/new.ts').exists())


if __name__ == '__main__':
    unittest.main()
