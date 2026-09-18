import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("compare_go_test_results.py")
SPEC = importlib.util.spec_from_file_location("compare_go_test_results", MODULE_PATH)
assert SPEC and SPEC.loader
COMPARE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(COMPARE)


def write_events(path: Path, events: list[dict[str, str]]) -> None:
    path.write_text(
        "".join(json.dumps(event) + "\n" for event in events),
        encoding="utf-8",
    )


class GoTestBaselineComparisonTest(unittest.TestCase):
    def test_existing_upstream_failure_does_not_block(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            baseline = root / "baseline.jsonl"
            candidate = root / "candidate.jsonl"
            event = {"Action": "fail", "Package": "example/pkg", "Test": "TestUpstream"}
            write_events(baseline, [event])
            write_events(candidate, [event])
            self.assertEqual(0, COMPARE.compare(baseline, candidate))

    def test_new_failure_blocks_and_reports_output(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            baseline = root / "baseline.jsonl"
            candidate = root / "candidate.jsonl"
            write_events(baseline, [])
            write_events(
                candidate,
                [
                    {
                        "Action": "output",
                        "Package": "example/pkg",
                        "Test": "TestNew",
                        "Output": "expected old, got new\n",
                    },
                    {"Action": "fail", "Package": "example/pkg", "Test": "TestNew"},
                ],
            )
            self.assertEqual(1, COMPARE.compare(baseline, candidate))

    def test_unreliable_baseline_never_allows_candidate(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            baseline = root / "baseline.jsonl"
            candidate = root / "candidate.jsonl"
            command_failure = {
                "Action": "fail",
                "Package": "[command:patch-relevant-upstream]",
            }
            write_events(baseline, [command_failure])
            write_events(candidate, [command_failure])
            self.assertEqual(2, COMPARE.compare(baseline, candidate))


if __name__ == "__main__":
    unittest.main()
