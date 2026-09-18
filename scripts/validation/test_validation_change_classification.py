import importlib.util
import sys
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("classify_validation_changes.py")
SPEC = importlib.util.spec_from_file_location("classify_validation_changes", MODULE_PATH)
assert SPEC and SPEC.loader
CLASSIFIER = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = CLASSIFIER
SPEC.loader.exec_module(CLASSIFIER)


class ValidationChangeClassificationTests(unittest.TestCase):
    def test_documentation_only_runs_no_component_builds(self) -> None:
        decision = CLASSIFIER.classify(["README.md", "docs/validation.md"])
        self.assertFalse(decision.core)
        self.assertFalse(decision.management)
        self.assertFalse(decision.source_image)

    def test_management_change_is_scoped(self) -> None:
        decision = CLASSIFIER.classify(
            ["cliproxyapi-pro-management/overlay/src/App.tsx"]
        )
        self.assertFalse(decision.core)
        self.assertTrue(decision.management)
        self.assertFalse(decision.source_image)

    def test_core_change_also_builds_source_image(self) -> None:
        for path in (
            "cliproxyapi-pro-core/patches/apply_upstream_patches.py",
            "scripts/validation/fixtures/antigravity_models_timeout_cleanup.patch",
        ):
            with self.subTest(path=path):
                decision = CLASSIFIER.classify([path])
                self.assertTrue(decision.core)
                self.assertFalse(decision.management)
                self.assertTrue(decision.source_image)

    def test_unknown_or_workflow_change_runs_everything(self) -> None:
        for path in (".github/workflows/ci.yml", "new-area/config.json"):
            with self.subTest(path=path):
                decision = CLASSIFIER.classify([path])
                self.assertTrue(decision.core)
                self.assertTrue(decision.management)
                self.assertTrue(decision.source_image)

    def test_upstream_inputs_are_independent_triggers(self) -> None:
        core = CLASSIFIER.classify([], core_upstream_changed=True)
        self.assertTrue(core.core)
        self.assertTrue(core.source_image)
        management = CLASSIFIER.classify([], management_upstream_changed=True)
        self.assertTrue(management.management)
        image = CLASSIFIER.classify([], image_input_changed=True)
        self.assertTrue(image.source_image)

    def test_missing_trusted_state_runs_everything(self) -> None:
        decision = CLASSIFIER.classify(["README.md"], force_all=True)
        self.assertTrue(decision.core)
        self.assertTrue(decision.management)
        self.assertTrue(decision.source_image)


if __name__ == "__main__":
    unittest.main()
