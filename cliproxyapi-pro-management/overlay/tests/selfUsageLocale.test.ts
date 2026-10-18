import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import proLocales from '../src/pro/locales.generated.json';
import en from '../src/i18n/locales/en.json';
import { SelfUsagePage } from '../src/pro/pages/SelfUsagePage';
import { formatTokenCount } from '../src/pro/pages/selfUsagePresentation';

describe('public usage page localization', () => {
  test('renders the public entry and accessible fields in each Pro language', async () => {
    const i18n = createInstance();
    await i18n.init({
      resources: Object.fromEntries(
        Object.entries(proLocales).map(([language, resources]) => [
          language,
          { translation: { ...en, ...resources } },
        ])
      ),
      lng: 'en',
      fallbackLng: false,
      interpolation: { escapeValue: false },
    });
    const original = i18n.language;
    try {
      for (const [language, title, placeholder] of [
        ['en', 'API key usage', 'Enter your API key'],
        ['ru', 'Использование API-ключа', 'Введите ваш API-ключ'],
        ['zh-CN', '密钥用量查询', '请输入你的 API key'],
        ['zh-TW', '金鑰用量查詢', '請輸入你的 API key'],
        ['vi', 'Mức sử dụng API key', 'Nhập API key của bạn'],
        ['ko', 'API key 사용량', 'API key를 입력하세요'],
      ]) {
        await i18n.changeLanguage(language);
        const markup = renderToStaticMarkup(
          createElement(I18nextProvider, { i18n }, createElement(SelfUsagePage))
        );
        expect(markup).toContain(`<h1>${title}</h1>`);
        expect(markup).toContain(`placeholder="${placeholder}"`);
        expect(markup).not.toContain('self_usage.');
        if (language === 'en' || language === 'ru' || language === 'vi' || language === 'ko')
          expect(markup.replace(/<select[\s\S]*?<\/select>/g, '')).not.toMatch(/[\u3400-\u9fff]/);
      }
    } finally {
      await i18n.changeLanguage(original);
    }
  });

  test('formats token counts with the selected language', () => {
    expect(formatTokenCount(12345, 'ru')).toBe(new Intl.NumberFormat('ru').format(12345));
    expect(formatTokenCount(12345, 'en')).toBe('12,345');
  });
});
