import { Glob } from 'bun';
import ts from 'typescript';
import { describe, expect, test } from 'bun:test';
import { createInstance } from 'i18next';
import proLocales from '../src/pro/locales.generated.json';
import { apiKeyPolicyLocales } from '../src/pro/apiKeyPolicyLocales';
import upstreamEnglish from '../src/i18n/locales/en.json';
import { SUPPORTED_LANGUAGES } from '../src/utils/constants';

const flatten = (value: Record<string, unknown>, prefix = ''): Record<string, string> =>
  Object.fromEntries(
    Object.entries(value).flatMap(([key, item]) =>
      item && typeof item === 'object'
        ? Object.entries(flatten(item as Record<string, unknown>, `${prefix}${key}.`))
        : [[`${prefix}${key}`, item as string]]
    )
  );

describe('registered Pro translations', () => {
  test('literal translation calls resolve to an owned resource', async () => {
    const available = {
      ...flatten(upstreamEnglish),
      ...flatten(proLocales.en),
      ...flatten(apiKeyPolicyLocales.en),
    };
    const missing: string[] = [];
    for await (const path of new Glob('**/*.{ts,tsx}').scan({
      cwd: `${import.meta.dir}/../src/pro`,
      absolute: true,
    })) {
      const file = ts.createSourceFile(
        path,
        await Bun.file(path).text(),
        ts.ScriptTarget.Latest,
        true
      );
      const visit = (node: ts.Node) => {
        if (ts.isCallExpression(node) && ['t', 'i18n.t'].includes(node.expression.getText(file))) {
          const key = node.arguments[0];
          if (key && ts.isStringLiteral(key) && !(key.text in available)) missing.push(key.text);
        }
        ts.forEachChild(node, visit);
      };
      visit(file);
    }
    expect(missing).toEqual([]);
  });
  test('matches the actual upstream language list', () => {
    expect(Object.keys(proLocales).sort()).toEqual([...SUPPORTED_LANGUAGES].sort());
  });

  test('every supported language owns all Pro strings without fallback', async () => {
    const i18n = createInstance();
    await i18n.init({ resources: {}, fallbackLng: false });
    for (const [language, resources] of Object.entries(proLocales)) {
      i18n.addResourceBundle(language, 'translation', resources, true, true);
    }
    for (const [language, resources] of Object.entries(apiKeyPolicyLocales)) {
      i18n.addResourceBundle(language, 'translation', resources, true, true);
    }
    const expected = { ...flatten(proLocales.en), ...flatten(apiKeyPolicyLocales.en) };
    for (const language of SUPPORTED_LANGUAGES) {
      for (const [key, source] of Object.entries(expected)) {
        // Chinese has no grammatical plural forms; legacy plural aliases are optional.
        if (language.startsWith('zh') && key.endsWith('_plural')) continue;
        const value = i18n.getResource(language, 'translation', key);
        expect(typeof value, `${language}: ${key}`).toBe('string');
        expect(value.trim(), `${language}: ${key}`).not.toBe('');
        expect(
          [...value.matchAll(/{{\s*([^{}]+?)\s*}}/g)].map((match) => match[1]).sort(),
          `${language}: ${key}`
        ).toEqual([...source.matchAll(/{{\s*([^{}]+?)\s*}}/g)].map((match) => match[1]).sort());
      }
    }
  });
});
