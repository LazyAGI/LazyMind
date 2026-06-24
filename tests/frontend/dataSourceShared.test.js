import { describe, expect, it } from 'vitest';
import { normalizeDataSourceParseStatus } from '../../frontend/src/modules/dataSource/shared.ts';

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
