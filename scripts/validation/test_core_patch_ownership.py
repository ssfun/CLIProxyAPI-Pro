import csv
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SURFACE_PATH = ROOT / 'scripts/validation/contracts/core-upstream-modified-files.txt'
OWNERSHIP_PATH = ROOT / 'scripts/validation/contracts/core-upstream-modified-files.ownership.tsv'

ALLOWED_CATEGORIES = {
    'generic-host-hook',
    'upstream-generic-fix',
    'must-remain-host-patch',
}

EXPECTED_CATEGORY_COUNTS = {
    'generic-host-hook': 28,
    'upstream-generic-fix': 23,
    'must-remain-host-patch': 40,
}


def load_surface() -> list[str]:
    return [
        line.strip()
        for line in SURFACE_PATH.read_text().splitlines()
        if line.strip() and not line.startswith('#')
    ]


def load_ownership() -> list[dict[str, str]]:
    lines = [
        line
        for line in OWNERSHIP_PATH.read_text().splitlines()
        if line.strip() and not line.startswith('#')
    ]
    return list(
        csv.DictReader(
            lines,
            delimiter='\t',
            fieldnames=['path', 'category', 'area', 'next_action'],
        )
    )


class CorePatchOwnershipContractTest(unittest.TestCase):
    def test_classifies_every_latest_upstream_modified_file_once(self) -> None:
        surface = load_surface()
        ownership = load_ownership()
        paths = [row['path'] for row in ownership]

        self.assertEqual(91, len(surface))
        self.assertEqual(surface, paths)
        self.assertEqual(len(paths), len(set(paths)))

    def test_uses_only_reviewed_categories_and_actionable_metadata(self) -> None:
        for row in load_ownership():
            with self.subTest(path=row['path']):
                self.assertIn(row['category'], ALLOWED_CATEGORIES)
                self.assertTrue(row['area'].strip())
                self.assertTrue(row['next_action'].strip())

    def test_preserves_reviewed_latest_upstream_ownership_counts(self) -> None:
        counts = {category: 0 for category in ALLOWED_CATEGORIES}
        for row in load_ownership():
            counts[row['category']] += 1
        self.assertEqual(EXPECTED_CATEGORY_COUNTS, counts)

if __name__ == '__main__':
    unittest.main()
