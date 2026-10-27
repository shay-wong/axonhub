import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import i18next from 'i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';
import ts from 'typescript';

test('currency formatting is ready on initialization and retains defaults, locale and precision', async () => {
  const source = await readFile(new URL('./i18n.ts', import.meta.url), 'utf8');
  const ast = ts.createSourceFile('i18n.ts', source, ts.ScriptTarget.Latest, true);
  const body = ast.statements
    .filter((node) => !ts.isImportDeclaration(node) && !ts.isExportAssignment(node))
    .map((node) => node.getText(ast))
    .join('\n')
    .replace(/import\.meta\.glob\('\.\.\/locales\/(en|zh-CN)\/\*\.json', \{ eager: true \}\)/g, (_, locale) => `localeModules['${locale}']`);
  const localeModules = {};
  for (const locale of ['en', 'zh-CN']) {
    const currency = JSON.parse(await readFile(new URL(`../locales/${locale}/currencies.json`, import.meta.url), 'utf8'));
    localeModules[locale] = { currencies: { default: currency } };
  }
  const js = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const instance = i18next.createInstance();
  let initialPrice;
  instance.on('initialized', () => {
    initialPrice = instance.t('currencies.format', { val: 12.5 });
  });
  new Function('i18n', 'LanguageDetector', 'initReactI18next', 'localeModules', js)(instance, LanguageDetector, initReactI18next, localeModules);

  assert.equal(instance.isInitialized, true);
  assert.equal(initialPrice, '$12.50');
  assert.equal(instance.t('currencies.format', { val: 0.123456, currency: 'USD', locale: 'en-US', minimumFractionDigits: 6 }), '$0.123456');
  assert.equal(instance.t('currencies.format', { val: 12.345, currency: 'CAD', locale: 'en-US', minimumFractionDigits: 2, maximumFractionDigits: 2 }), '$12.35');
  await instance.changeLanguage('zh');
  assert.equal(instance.t('currencies.format', { val: 12.5, currency: 'CNY' }), '¥12.50');
  assert.equal(instance.t('currencies.format', { val: 12.5, currency: 'EUR', locale: 'de-DE' }), '12,50 €');
});
