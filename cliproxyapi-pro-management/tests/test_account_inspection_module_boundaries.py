import unittest
from pathlib import Path


OVERLAY_ROOT = Path(__file__).resolve().parents[1] / 'overlay/src'
PAGE_PATH = OVERLAY_ROOT / 'pages/AccountInspectionPage.tsx'
FEATURE_ROOT = OVERLAY_ROOT / 'features/monitoring'


class AccountInspectionModuleBoundariesTest(unittest.TestCase):
    def test_page_remains_a_workflow_controller(self) -> None:
        source = PAGE_PATH.read_text()

        self.assertLess(len(source.splitlines()), 2300)
        self.assertNotIn('const buildInspectionResultsViewState =', source)
        self.assertNotIn('const scheduleAuthFileAccountStats =', source)
        self.assertNotIn('const inspectionBackendReducer =', source)
        self.assertNotIn('function InspectionErrorDetailsPanel(', source)

    def test_feature_owns_page_model_and_styles(self) -> None:
        model = (FEATURE_ROOT / 'accountInspectionPageModel.tsx').read_text()
        self.assertIn('export const buildInspectionResultsViewState', model)
        self.assertIn('export const inspectionBackendReducer', model)
        self.assertTrue((FEATURE_ROOT / 'accountInspection.module.scss').is_file())
        self.assertFalse((OVERLAY_ROOT / 'pages/AccountInspectionPage.module.scss').exists())
        self.assertEqual(
            len(list((FEATURE_ROOT / 'account-inspection-styles').glob('*.scss'))),
            6,
        )


if __name__ == '__main__':
    unittest.main()
