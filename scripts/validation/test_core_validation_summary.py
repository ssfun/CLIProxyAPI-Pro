import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("summarize_core_validation.py")
SPEC = importlib.util.spec_from_file_location("summarize_core_validation", MODULE_PATH)
assert SPEC and SPEC.loader
SUMMARY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUMMARY)


class CoreValidationSummaryTests(unittest.TestCase):
    def test_renders_timings_and_slowest_packages(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            timings = root / "phase-timings.tsv"
            candidate = root / "go-test-candidate.jsonl"
            timings.write_text("candidate tests\t42\t0\nbuilds\t7\t0\n", encoding="utf-8")
            candidate.write_text(
                "\n".join(
                    [
                        json.dumps(
                            {
                                "Action": "pass",
                                "Package": "example/slow",
                                "Elapsed": 12.5,
                            }
                        ),
                        json.dumps(
                            {
                                "Action": "pass",
                                "Package": "example/fast",
                                "Elapsed": 1.25,
                            }
                        ),
                    ]
                )
                + "\n",
                encoding="utf-8",
            )

            output = SUMMARY.render(
                SUMMARY.read_timings(timings),
                SUMMARY.read_package_durations([candidate]),
            )
            self.assertIn("| candidate tests | 42 | 0 |", output)
            self.assertLess(output.index("example/slow"), output.index("example/fast"))


if __name__ == "__main__":
    unittest.main()
