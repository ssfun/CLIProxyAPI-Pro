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

    def test_preserves_both_retained_claude_render_branches_and_reapplication(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            cases = (
                ('src/features/quota/components/QuotaCard.tsx', QUOTA_CARD_SOURCE, 'quotaClasses', 'status', 'quota.cachedAt'),
                ('src/features/authFiles/components/AuthFileQuotaSection.tsx', AUTH_FILE_QUOTA_SECTION_SOURCE, 'compactQuotaClasses', 'quotaStatus', "'cachedAt' in quota ? quota.cachedAt : undefined"),
            )
            for relative_path, source, classes, _, _ in cases:
                path = target / relative_path
                path.parent.mkdir(parents=True, exist_ok=True)
                source = source.replace(') : quota ? (', ') : quota && !retainedClaudeUsage ? (')
                source = source.replace(') : (', ') : !retainedClaudeUsage ? (')
                indent = '        ' if classes == 'quotaClasses' else '      '
                source = source.replace('    </div>', f'{indent}{{retainedClaudeUsage && quota && <adapter.Body quota={{quota}} classes={{{classes}}} />}}\n    </div>')
                path.write_text(source)

            CUSTOMIZATIONS.patch_quota_cards_latest(target)
            CUSTOMIZATIONS.flush_writes()
            first_application = {}
            for relative_path, _, classes, status, cached_at in cases:
                path = target / relative_path
                source = path.read_text()
                first_application[path] = source
                self.assertEqual(source.count("import { QuotaCachedTime }"), 1)
                self.assertEqual(source.count(f'<adapter.Body quota={{quota}} classes={{{classes}}} />'), 2)
                self.assertEqual(source.count(f'<QuotaCachedTime quotaStatus={{{status}}} cachedAt={{{cached_at}}} />'), 2)
                self.assertIn(') : quota && !retainedClaudeUsage ? (', source)
                self.assertIn(') : !retainedClaudeUsage ? (', source)
                self.assertIn('{retainedClaudeUsage && quota && (', source)

            CUSTOMIZATIONS.patch_quota_cards_latest(target)
            CUSTOMIZATIONS.flush_writes()
            for path, source in first_application.items():
                self.assertEqual(path.read_text(), source)

    def test_multiline_plan_bodies_preserve_properties_guards_and_reapplication(self) -> None:
        for broken in (None, 'missing', 'duplicate', 'property', 'guard'):
            with self.subTest(broken=broken), tempfile.TemporaryDirectory() as temp_dir:
                CUSTOMIZATIONS._writes.clear()
                target = Path(temp_dir)
                originals = {}
                for relative, source, classes, indent in (
                    ('src/features/quota/components/QuotaCard.tsx', QUOTA_CARD_SOURCE, 'quotaClasses', '          '),
                    ('src/features/authFiles/components/AuthFileQuotaSection.tsx', AUTH_FILE_QUOTA_SECTION_SOURCE, 'compactQuotaClasses', '        '),
                ):
                    body = ('<adapter.Body\n'
                            + indent + '  quota={quota}\n'
                            + indent + f'  classes={{{classes}}}\n'
                            + indent + '  planItems={claudePlanItems}\n'
                            + indent + '  afterPlan={claudeAfterPlan}\n'
                            + indent + '/>')
                    source = source.replace(') : quota ? (', ') : quota && !retainedClaudeUsage ? (')
                    source = source.replace(f'<adapter.Body quota={{quota}} classes={{{classes}}} />', body)
                    retained = indent[:-2] + '{retainedClaudeUsage && quota && (\n' + indent + body + '\n' + indent[:-2] + ')}\n'
                    loading = indent[:-2] + '{loading && retainedClaudeUsage && <div role="status">Loading</div>}\n'
                    source = source.replace('    </div>', loading + retained + '    </div>')
                    if broken == 'missing':
                        source = source.replace(body, '', 1)
                    elif broken == 'duplicate':
                        source = source.replace(body, body + '\n' + indent + body, 1)
                    elif broken == 'property':
                        source = source.replace('afterPlan={claudeAfterPlan}', 'afterPlan={unknownPlan}', 1)
                    elif broken == 'guard':
                        source = source.replace('{retainedClaudeUsage && quota && (', '{retainedClaudeUsage && (')
                    path = target / relative
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text(source)
                    originals[path] = source
                if broken:
                    with self.assertRaises(RuntimeError):
                        CUSTOMIZATIONS.patch_quota_cards_latest(target)
                    for path, source in originals.items():
                        self.assertEqual(path.read_text(), source)
                    continue
                CUSTOMIZATIONS.patch_quota_cards_latest(target)
                CUSTOMIZATIONS.flush_writes()
                first = {path: path.read_text() for path in originals}
                for source in first.values():
                    self.assertEqual(source.count('planItems={claudePlanItems}'), 2)
                    self.assertEqual(source.count('afterPlan={claudeAfterPlan}'), 2)
                    self.assertEqual(source.count('<QuotaCachedTime '), 2)
                    self.assertEqual(source.count('<adapter.Body'), 2)
                    self.assertIn(') : quota && !retainedClaudeUsage ? (', source)
                    self.assertIn('{loading && retainedClaudeUsage && <div role="status">Loading</div>}', source)
                    self.assertIn('{retainedClaudeUsage && quota && (', source)
                CUSTOMIZATIONS.patch_quota_cards_latest(target)
                CUSTOMIZATIONS.flush_writes()
                for path, source in first.items():
                    self.assertEqual(path.read_text(), source)

    def test_rejects_missing_or_duplicate_body_without_flushing(self) -> None:
        for broken_component in ('card', 'auth'):
            for body_count in (0, 2):
                with self.subTest(component=broken_component, body_count=body_count), tempfile.TemporaryDirectory() as temp_dir:
                    CUSTOMIZATIONS._writes.clear()
                    target = Path(temp_dir)
                    cases = (
                        ('card', 'src/features/quota/components/QuotaCard.tsx', QUOTA_CARD_SOURCE, 'quotaClasses'),
                        ('auth', 'src/features/authFiles/components/AuthFileQuotaSection.tsx', AUTH_FILE_QUOTA_SECTION_SOURCE, 'compactQuotaClasses'),
                    )
                    originals = {}
                    for component, relative_path, source, classes in cases:
                        path = target / relative_path
                        path.parent.mkdir(parents=True, exist_ok=True)
                        if component == broken_component:
                            body = f'<adapter.Body quota={{quota}} classes={{{classes}}} />'
                            indent = '          ' if classes == 'quotaClasses' else '        '
                            source = source.replace(body, ('\n' + indent).join([body] * body_count))
                        path.write_text(source)
                        originals[path] = source
                    with self.assertRaisesRegex(RuntimeError, f'Expected one pattern.*found {body_count}'):
                        CUSTOMIZATIONS.patch_quota_cards_latest(target)
                    for path, source in originals.items():
                        self.assertEqual(path.read_text(), source)

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

    def test_timestamps_success_states_at_adapter_registry_without_changing_hooks(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            hooks_dir = target / 'src/features/quota/hooks'
            hooks_dir.mkdir(parents=True)
            actions_path = hooks_dir / 'useQuotaActions.ts'
            batch_path = hooks_dir / 'useQuotaBatchLoader.ts'
            providers_path = target / 'src/features/quota/providers/index.ts'
            providers_path.parent.mkdir(parents=True)
            providers_path.write_text("import { useQuotaStore } from '@/stores';\n\nexport const QUOTA_ADAPTERS = {};\n\nexport type QuotaMapUpdater = (\n")
            actions_path.write_text(QUOTA_ACTIONS_SOURCE)
            batch_path.write_text(QUOTA_BATCH_SOURCE)

            CUSTOMIZATIONS.patch_quota_success_timestamps(target)
            CUSTOMIZATIONS.flush_writes()

            actions = actions_path.read_text()
            batch = batch_path.read_text()
            providers = providers_path.read_text()
            self.assertEqual(QUOTA_ACTIONS_SOURCE, actions)
            self.assertEqual(QUOTA_BATCH_SOURCE, batch)
            self.assertIn("import { withQuotaCachedAt } from '@/pro/shared/quotaState';", providers)
            self.assertIn('for (const adapter of Object.values(QUOTA_ADAPTERS))', providers)
            self.assertIn('adapter.buildSuccessState = (data) => withQuotaCachedAt(buildSuccessState(data));', providers)
            self.assertNotIn("providers/antigravity/data", providers)

            CUSTOMIZATIONS.patch_quota_success_timestamps(target)
            CUSTOMIZATIONS.flush_writes()
            self.assertEqual(actions, actions_path.read_text())
            self.assertEqual(batch, batch_path.read_text())
            self.assertEqual(providers, providers_path.read_text())


if __name__ == '__main__':
    unittest.main()
