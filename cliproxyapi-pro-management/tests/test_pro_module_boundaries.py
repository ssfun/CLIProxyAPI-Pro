import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
MODULES = ROOT / 'overlay/src/pro/modules'
OVERLAY_SRC = ROOT / 'overlay/src'
CUSTOMIZER = ROOT / 'apply_customizations.py'
IMPORT_PATTERN = re.compile(r"@/pro/modules/([^/'\"]+)([^'\"]*)")
RELATIVE_IMPORT_PATTERN = re.compile(
    r"(?:from\s+|import\s*\()\s*['\"](\.{1,2}/[^'\"]+)['\"]"
)
SERVICES_API_BARREL_PATTERN = re.compile(r"from\s+['\"]@/services/api['\"]")


class ProModuleBoundaryTests(unittest.TestCase):
    def test_cross_module_imports_use_public_index_only(self):
        violations = []
        for path in sorted(MODULES.rglob('*')):
            if path.suffix not in {'.ts', '.tsx'}:
                continue
            owner = path.relative_to(MODULES).parts[0]
            source = path.read_text(encoding='utf-8')
            for match in IMPORT_PATTERN.finditer(source):
                dependency, suffix = match.groups()
                if dependency != owner and suffix:
                    violations.append(
                        f'{path.relative_to(ROOT)} imports private path '
                        f'@/pro/modules/{dependency}{suffix}'
                    )
        self.assertEqual([], violations, '\n'.join(violations))

    def test_host_integrations_use_public_index_only(self):
        violations = []
        sources = [CUSTOMIZER]
        sources.extend(
            path
            for path in sorted(OVERLAY_SRC.rglob('*'))
            if path.suffix in {'.ts', '.tsx'} and MODULES not in path.parents
        )
        for path in sources:
            source = path.read_text(encoding='utf-8')
            for match in IMPORT_PATTERN.finditer(source):
                dependency, suffix = match.groups()
                if suffix:
                    violations.append(
                        f'{path.relative_to(ROOT)} imports private path '
                        f'@/pro/modules/{dependency}{suffix}'
                    )
        self.assertEqual([], violations, '\n'.join(violations))

    def test_relative_imports_do_not_escape_module_root(self):
        violations = []
        for path in sorted(MODULES.rglob('*')):
            if path.suffix not in {'.ts', '.tsx'}:
                continue
            owner_root = MODULES / path.relative_to(MODULES).parts[0]
            source = path.read_text(encoding='utf-8')
            for match in RELATIVE_IMPORT_PATTERN.finditer(source):
                target = (path.parent / match.group(1)).resolve()
                if target != owner_root.resolve() and owner_root.resolve() not in target.parents:
                    violations.append(
                        f'{path.relative_to(ROOT)} escapes its module with {match.group(1)}'
                    )
        self.assertEqual([], violations, '\n'.join(violations))

    def test_shared_layer_does_not_depend_on_feature_modules(self):
        violations = []
        shared = OVERLAY_SRC / 'pro/shared'
        for path in sorted(shared.rglob('*')):
            if path.suffix not in {'.ts', '.tsx'}:
                continue
            if IMPORT_PATTERN.search(path.read_text(encoding='utf-8')):
                violations.append(f'{path.relative_to(ROOT)} imports a feature module')
        self.assertEqual([], violations, '\n'.join(violations))

    def test_modules_do_not_import_services_api_barrel(self):
        violations = []
        for path in sorted(MODULES.rglob('*')):
            if path.suffix not in {'.ts', '.tsx'}:
                continue
            if SERVICES_API_BARREL_PATTERN.search(path.read_text(encoding='utf-8')):
                violations.append(f'{path.relative_to(ROOT)} imports @/services/api barrel')
        self.assertEqual([], violations, '\n'.join(violations))

    def test_public_module_indexes_use_explicit_exports(self):
        violations = []
        for path in sorted(MODULES.glob('*/index.ts')):
            if re.search(r'^\s*export\s+\*', path.read_text(encoding='utf-8'), re.MULTILINE):
                violations.append(f'{path.relative_to(ROOT)} uses export *')
        self.assertEqual([], violations, '\n'.join(violations))

    def test_registry_derives_host_surfaces_from_module_manifests(self):
        registry = (OVERLAY_SRC / 'pro/registry.tsx').read_text(encoding='utf-8')
        bootstrap = (OVERLAY_SRC / 'pro/ProBootstrap.tsx').read_text(encoding='utf-8')
        self.assertIn('const proModules = [', registry)
        self.assertIn('proModules.flatMap((module)', registry)
        self.assertIn('const navigationGroups = new Map', registry)
        self.assertNotRegex(registry, r"path:\s*['\"]/[^'\"]+['\"]")
        self.assertIn("import { proBootstraps } from '@/pro/registry'", bootstrap)
        self.assertNotIn('QuotaPersistenceBootstrap', bootstrap)

        module_list_start = registry.index('const proModules = [')
        module_list = registry[
            module_list_start:registry.index('];', module_list_start)
        ]
        self.assertEqual(
            [
                'monitoringModule',
                'inspectionModule',
                'apiKeyPolicyModule',
                'oauthPolicyModule',
                'proxyPoolModule',
                'dataManagementModule',
                'quotaModule',
            ],
            re.findall(r'^\s{2}(\w+Module),$', module_list, re.MULTILINE),
        )

        for module in ('monitoring', 'inspection', 'proxyPool', 'oauthPolicy', 'dataManagement', 'quota'):
            index = (MODULES / module / 'index.ts').read_text(encoding='utf-8')
            self.assertIn("from './manifest'", index)


if __name__ == '__main__':
    unittest.main()
