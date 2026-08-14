import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('scheduled task name validation', () => {
  it('requires a non-blank task name before creating a schedule', () => {
    const scheduleList = readFileSync(
      new URL('../../frontend/src/modules/taskCenter/ScheduleList.tsx', import.meta.url),
      'utf8',
    );
    const api = readFileSync(
      new URL('../../frontend/src/modules/taskCenter/api.ts', import.meta.url),
      'utf8',
    );
    const createRequest = api.match(
      /export interface CreateScheduleRequest \{([\s\S]*?)\n\}/,
    )?.[1];

    expect(scheduleList).toContain(
      "rules={[{ required: true, whitespace: true, message: t('taskCenter.scheduleNameRequired') }]}",
    );
    expect(scheduleList).toContain('name: values.name.trim()');
    expect(createRequest).toContain('name: string;');
    expect(createRequest).not.toContain('name?: string;');
  });

  it('provides the validation message in both supported locales', () => {
    const expectedCopy = {
      'zh-CN': 'scheduleNameRequired: "请输入任务名称"',
      'en-US': 'scheduleNameRequired: "Please enter a task name"',
    };

    for (const [locale, snippet] of Object.entries(expectedCopy)) {
      const translations = readFileSync(
        new URL(`../../frontend/src/i18n/locales/${locale}.ts`, import.meta.url),
        'utf8',
      );
      expect(translations).toContain(snippet);
    }
  });
});
