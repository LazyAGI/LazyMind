import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('skill upload copy', () => {
  it('mentions only .zip in the upload menu and package-type message', () => {
    const expectedCopy = {
      'zh-CN': [
        'memorySkillCreateUploadDesc: "上传本地文件（.zip）"',
        'memorySkillUploadPackageTypeError: "仅支持 .zip 压缩包"',
      ],
      'en-US': [
        'memorySkillCreateUploadDesc: "Upload a local file (.zip)"',
        'memorySkillUploadPackageTypeError: "Only .zip archives are supported"',
      ],
    };

    for (const [locale, snippets] of Object.entries(expectedCopy)) {
      const translations = readFileSync(
        new URL(`../../frontend/src/i18n/locales/${locale}.ts`, import.meta.url),
        'utf8',
      );
      for (const snippet of snippets) {
        expect(translations).toContain(snippet);
      }
    }
  });
});
