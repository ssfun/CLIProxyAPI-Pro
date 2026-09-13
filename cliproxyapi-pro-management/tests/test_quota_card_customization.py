import importlib.util
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / 'apply_customizations.py'
SPEC = importlib.util.spec_from_file_location('apply_customizations', MODULE_PATH)
assert SPEC and SPEC.loader
CUSTOMIZATIONS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CUSTOMIZATIONS)


QUOTA_CARD_SOURCE = """import { resolveQuotaErrorMessage } from '@/utils/quota';

export function QuotaCard() {
  return (
    <div>
        ) : quota ? (
          <adapter.Body quota={quota} classes={quotaClasses} />
        ) : (
    </div>
  );
}
"""

AUTH_FILE_QUOTA_SECTION_SOURCE = """import { bindQuotaClasses } from '@/features/quota/types';

export function AuthFileQuotaSection() {
  return (
    <div>
      ) : quota ? (
        <adapter.Body quota={quota} classes={compactQuotaClasses} />
      ) : (
    </div>
  );
}
"""

QUOTA_ACTIONS_SOURCE = """import { getStatusFromError } from '@/utils/quota';

export function useQuotaActions() {
  const refresh = () => adapter.buildSuccessState(data);
  const reset = () => adapter.buildSuccessState(data);
  return { refresh, reset };
}
"""

QUOTA_BATCH_SOURCE = """import { getStatusFromError } from '@/utils/quota';

export function useQuotaBatchLoader() {
  return adapter.buildSuccessState(result.data);
}
"""


class QuotaCardCustomizationTest(unittest.TestCase):
    def setUp(self) -> None:
        CUSTOMIZATIONS._writes.clear()

    def test_adds_cached_time_to_quota_renderers(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            quota_dir = target / 'src/features/quota/components'
            auth_dir = target / 'src/features/authFiles/components'
            quota_dir.mkdir(parents=True)
            auth_dir.mkdir(parents=True)
            quota_path = quota_dir / 'QuotaCard.tsx'
            auth_path = auth_dir / 'AuthFileQuotaSection.tsx'
            quota_path.write_text(QUOTA_CARD_SOURCE)
            auth_path.write_text(AUTH_FILE_QUOTA_SECTION_SOURCE)

            CUSTOMIZATIONS.patch_quota_cards_latest(target)
            CUSTOMIZATIONS.flush_writes()

            quota_source = quota_path.read_text()
            auth_source = auth_path.read_text()
            self.assertIn("import { QuotaCachedTime } from '@/pro/modules/quota';", quota_source)
            self.assertIn('<QuotaCachedTime quotaStatus={status} cachedAt={quota.cachedAt} />', quota_source)
            self.assertIn("import { QuotaCachedTime } from '@/pro/modules/quota';", auth_source)
            self.assertIn("<QuotaCachedTime quotaStatus={quotaStatus} cachedAt={'cachedAt' in quota ? quota.cachedAt : undefined} />", auth_source)

            CUSTOMIZATIONS.patch_quota_cards_latest(target)
            CUSTOMIZATIONS.flush_writes()
            self.assertEqual(quota_source, quota_path.read_text())
            self.assertEqual(auth_source, auth_path.read_text())

    def test_timestamps_all_success_states_at_shared_commit_boundaries(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            hooks_dir = target / 'src/features/quota/hooks'
            hooks_dir.mkdir(parents=True)
            actions_path = hooks_dir / 'useQuotaActions.ts'
            batch_path = hooks_dir / 'useQuotaBatchLoader.ts'
            actions_path.write_text(QUOTA_ACTIONS_SOURCE)
            batch_path.write_text(QUOTA_BATCH_SOURCE)

            CUSTOMIZATIONS.patch_quota_success_timestamps(target)
            CUSTOMIZATIONS.flush_writes()

            actions = actions_path.read_text()
            batch = batch_path.read_text()
            self.assertEqual(2, actions.count('withQuotaCachedAt(adapter.buildSuccessState(data))'))
            self.assertIn(
                'withQuotaCachedAt(adapter.buildSuccessState(result.data))',
                batch,
            )
            self.assertNotIn("providers/antigravity/data", actions + batch)

            CUSTOMIZATIONS.patch_quota_success_timestamps(target)
            CUSTOMIZATIONS.flush_writes()
            self.assertEqual(actions, actions_path.read_text())
            self.assertEqual(batch, batch_path.read_text())


if __name__ == '__main__':
    unittest.main()
