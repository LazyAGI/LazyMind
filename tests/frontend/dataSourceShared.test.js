import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { normalizeDataSourceParseStatus } from '../../frontend/src/modules/dataSource/utils/status.ts';

describe('data source parse status normalization', () => {
  it('shows cloud queued and running work as downloading', () => {
    expect(
      normalizeDataSourceParseStatus('QUEUED PENDING', undefined, {
        sourceType: 'feishu',
      }),
    ).toBe('downloading');
    expect(
      normalizeDataSourceParseStatus('RUNNING PENDING', undefined, {
        sourceType: 'notion',
      }),
    ).toBe('downloading');
  });

  it('does not expose downloading for local sources', () => {
    expect(
      normalizeDataSourceParseStatus('QUEUED PENDING', undefined, {
        sourceType: 'local',
      }),
    ).toBe('reindexing');
  });

  it('keeps submitted cloud work in parsing status', () => {
    expect(
      normalizeDataSourceParseStatus('SUBMITTED PENDING', undefined, {
        sourceType: 'feishu',
      }),
    ).toBe('reindexing');
  });

  it('normalizes effective statuses returned by the backend', () => {
    expect(
      normalizeDataSourceParseStatus('DOWNLOADING', undefined, {
        sourceType: 'feishu',
      }),
    ).toBe('downloading');
    expect(
      normalizeDataSourceParseStatus('DOWNLOAD_FAILED', undefined, {
        sourceType: 'feishu',
      }),
    ).toBe('download_failed');
    expect(
      normalizeDataSourceParseStatus('PARSE_FAILED', undefined, {
        sourceType: 'feishu',
      }),
    ).toBe('parse_failed');
  });

  it('shows download failure only for cloud sources', () => {
    const lastError = { phase: 'download', code: 'PERMISSION_DENIED' };

    expect(
      normalizeDataSourceParseStatus('FAILED', lastError, {
        sourceType: 'feishu',
      }),
    ).toBe('download_failed');
    expect(
      normalizeDataSourceParseStatus('FAILED', lastError, {
        sourceType: 'local',
      }),
    ).toBe('failed');
  });
});

describe('cloud OAuth provider wording', () => {
  it('renders callback status with the active provider name', () => {
    const callbackSource = readFileSync(
      new URL('../../frontend/src/modules/dataSource/common/feishuCallback.tsx', import.meta.url),
      'utf8',
    );
    const zhSource = readFileSync(
      new URL('../../frontend/src/i18n/locales/zh-CN.ts', import.meta.url),
      'utf8',
    );

    expect(callbackSource).toContain('notion: "admin.dataSourceTypeNotion"');
    expect(callbackSource).toContain('dataSourceCallbackSuccessTitle", { providerName }');
    expect(zhSource).toContain('dataSourceCallbackSuccessTitle: "{{providerName}}账号已连接"');
    expect(zhSource).not.toContain('dataSourceCallbackSuccessTitle: "飞书账号已连接"');
  });

  it('builds the Notion callback URL from the current application origin', () => {
    const guideSource = readFileSync(
      new URL('../../frontend/src/modules/modelProvider/pages/NotionSetupGuide.tsx', import.meta.url),
      'utf8',
    );

    expect(guideSource).toContain('getCloudDataSourceCallbackUrl("notion")');
    expect(guideSource).not.toContain('http://127.0.0.1:8090/oauth/notion');
  });
});
