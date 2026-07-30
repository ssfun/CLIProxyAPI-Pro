import importlib.util
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / 'apply_customizations.py'
SPEC = importlib.util.spec_from_file_location('apply_customizations', MODULE_PATH)
assert SPEC and SPEC.loader
CUSTOMIZATIONS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CUSTOMIZATIONS)


QUOTA_CARD_TEMPLATE = """import { TYPE_COLORS%s } from '@/utils/quota';
import { QuotaProgressBar, type QuotaProgressBarProps } from './QuotaProgressBar';
import styles from '@/pages/QuotaPage.module.scss';

export interface QuotaStatusState {
  status: QuotaStatus;
  error?: string;
  errorStatus?: number;
}

export function QuotaCard() {
  return (
    <div>
        ) : quota ? (
          %s
        ) : (
    </div>
  );
}
"""


class QuotaCardCustomizationTest(unittest.TestCase):
    def setUp(self) -> None:
        CUSTOMIZATIONS._writes.clear()

    def assert_variant_is_customized(self, import_suffix: str, render_call: str) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            quota_dir = target / 'src/components/quota'
            quota_dir.mkdir(parents=True)
            path = quota_dir / 'QuotaCard.tsx'
            path.write_text(QUOTA_CARD_TEMPLATE % (import_suffix, render_call))

            CUSTOMIZATIONS.patch_quota_card(target)
            CUSTOMIZATIONS.flush_writes()

            source = path.read_text()
            self.assertIn("import { QuotaCachedTime } from '@/extensions/quota/QuotaCardExtras';", source)
            self.assertIn('cachedAt?: number;', source)
            self.assertIn(f'{{{render_call}}}', source)
            self.assertIn('<QuotaCachedTime quotaStatus={quotaStatus} cachedAt={quota.cachedAt} />', source)

            CUSTOMIZATIONS.patch_quota_card(target)
            CUSTOMIZATIONS.flush_writes()
            self.assertEqual(source, path.read_text())

    def test_supports_legacy_quota_card_renderer(self) -> None:
        self.assert_variant_is_customized(
            '',
            'renderQuotaItems(quota, t, { styles, QuotaProgressBar })',
        )

    def test_supports_upstream_bound_quota_progress_bar_renderer(self) -> None:
        self.assert_variant_is_customized(
            ', resolveQuotaErrorMessage',
            'renderQuotaItems(quota, t, { styles, QuotaProgressBar: BoundQuotaProgressBar })',
        )


if __name__ == '__main__':
    unittest.main()
