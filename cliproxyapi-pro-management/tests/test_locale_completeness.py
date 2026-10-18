import importlib.util
import json
import re
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def flatten(value, prefix=''):
    return {
        nested_key: nested_value
        for key, item in value.items()
        for nested_key, nested_value in (
            flatten(item, f'{prefix}{key}.').items()
            if isinstance(item, dict) else [(f'{prefix}{key}', item)]
        )
    }


class LocaleCompletenessTest(unittest.TestCase):
    def test_pro_locales_have_matching_keys_and_interpolation_variables(self):
        locales = {
            language: flatten(resources)
            for language, resources in json.loads((ROOT / 'monitoring-locales.json').read_text()).items()
        }
        self.assertEqual(
            set(locales), {'en.json', 'ru.json', 'zh-CN.json', 'zh-TW.json', 'vi.json', 'ko.json'}
        )
        all_keys = set().union(*(set(resources) for resources in locales.values()))
        reference = {
            key: next(resources[key] for resources in locales.values() if key in resources)
            for key in all_keys
        }
        for language, resources in locales.items():
            with self.subTest(language=language):
                missing = set(locales['zh-CN.json']) - resources.keys()
                self.assertFalse(missing, f'{language} missing {sorted(missing)}')
                for key, value in resources.items():
                    self.assertIsInstance(value, str, f'{language}: {key}')
                    self.assertTrue(value.strip(), f'{language}: {key}')
                    self.assertEqual(
                        sorted(re.findall(r'{{\s*([^{}]+?)\s*}}', value)),
                        sorted(re.findall(r'{{\s*([^{}]+?)\s*}}', reference[key])),
                        f'{language}: {key}',
                    )

    def test_generated_locales_match_upstream_language_inventory(self):
        spec = importlib.util.spec_from_file_location(
            'locale_customizations', ROOT / 'apply_customizations.py'
        )
        customizations = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(customizations)
        languages = ('en', 'ru', 'zh-CN', 'zh-TW', 'vi', 'ko')
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory)
            locale_dir = target / 'src/i18n/locales'
            locale_dir.mkdir(parents=True)
            for language in languages:
                fixture = (
                    '{\n  "claude_quota": {\n'
                    '    "seven_day_fable": "Fable 5 · 7 ngày",\n'
                    '    "extra_usage_label": "test"\n  }\n}\n'
                    if language == 'vi' else '{}'
                )
                (locale_dir / f'{language}.json').write_text(fixture)
            customizations.patch_locales(target)
            customizations.flush_writes()
            generated_path = target / 'src/pro/locales.generated.json'
            generated = json.loads(generated_path.read_text())
            self.assertEqual(set(generated), set(languages))
            for language in languages:
                for key in ('title', 'tab_backups', 'cleanup_confirm_message'):
                    self.assertTrue(generated[language]['data_management'][key])
                self.assertTrue(generated[language]['login']['self_usage_link'])
            original = generated_path.read_text()
            customizations.patch_locales(target)
            customizations.flush_writes()
            self.assertEqual(generated_path.read_text(), original)


if __name__ == '__main__':
    unittest.main()
