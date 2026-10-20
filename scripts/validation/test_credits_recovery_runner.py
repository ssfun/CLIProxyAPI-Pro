"""Exercise the runner's process/event boundary before changing its pass gate."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

RUNNER = Path(__file__).parent / 'e2e/antigravity-credits-recovery/run.py'
REQUIRED = ['TestProCreditsRecoveryPublicationOrdering',
            'TestAntigravityManagerExecuteAndStream_ToppedUpCreditsRecoverDuringQuotaCooldown']


class CreditsRecoveryRunnerTests(unittest.TestCase):
    def test_required_top_level_test_outcomes(self):
        both = [{'Action': 'pass', 'Test': name} for name in REQUIRED]
        cases = [('both_pass', both, 0, True), ('process_failure', both, 1, False),
                 ('failed_event', both + [{'Action': 'fail', 'Test': 'child'}], 0, False)]
        for index in range(2):
            cases.append(('missing_' + str(index), [both[1-index]], 0, False))
            cases.append(('skipped_' + str(index), [both[1-index],
                          {'Action': 'skip', 'Test': REQUIRED[index]}], 0, False))
        for name, events, process_code, should_pass in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                source = root / 'core/internal/runtime/executor'
                source.mkdir(parents=True)
                (source / 'antigravity_executor_credits.go').write_text(
                    '\tif hint, ok := cliproxyauth.GetAntigravityCreditsHint(authID); ok && hint.Known && !hint.Available {\n')
                fake = root / 'go'
                fake.write_text('#!' + sys.executable + '\nimport os,sys\n'
                                'print(os.environ["CREDITS_GATE_EVENTS"])\n'
                                'sys.exit(int(os.environ["CREDITS_GATE_EXIT"]))\n')
                fake.chmod(0o700)
                env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ.get('PATH', ''),
                           CREDITS_GATE_EVENTS='\n'.join(json.dumps(e) for e in events),
                           CREDITS_GATE_EXIT=str(process_code))
                result = subprocess.run([sys.executable, str(RUNNER), '--src', str(root / 'core'),
                                         '--output', str(root / 'output')], env=env,
                                        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                receipt = json.loads((root / 'output/result.json').read_text())
                self.assertEqual(result.returncode == 0, should_pass, result.stdout + result.stderr)
                self.assertEqual(receipt['exit_code'] == 0, should_pass)


if __name__ == '__main__':
    unittest.main()
