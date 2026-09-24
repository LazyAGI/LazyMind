import { describe, expect, it } from 'vitest';
import { documentPublicationTargetUrl, documentPublicationUrl } from './documentPublicationUrl';

describe('document publication links', () => {
  it.each([
    ['feishu', { uri: 'feishu:/~doc/FixtureDoc' }, 'https://feishu.cn/docs/FixtureDoc'],
    ['notion', { doc_id: '12345678-1234-1234-1234-123456789abc' }, 'https://www.notion.so/12345678123412341234123456789abc'],
    ['feishu', { uri: 'feishu:/~docx/token?redirect=elsewhere' }, undefined],
    ['feishu', { doc_id: '../wrong-target' }, undefined],
    ['notion', { uri: 'notion:/~page/not-a-page-id' }, undefined],
    ['github', { uri: 'feishu:/~docx/FixtureDoc' }, undefined],
  ])('resolves only identified internal documents for %s', (provider, target, expected) => {
    expect(documentPublicationTargetUrl(target, provider)).toBe(expected);
  });
  it('does not present the WeChat console as the published draft', () => {
    expect(documentPublicationTargetUrl({ meta: { browser_url: 'https://mp.weixin.qq.com/' } }, 'wechat')).toBeUndefined();
  });
  it.each([
    'javascript:alert(1)', 'file:///tmp/note.md', 'https://user:password@example.test',
    'obsidian://vlt_fixture/note.md', 'obsidian://new?path=%2Ftmp%2Fnote.md',
    'obsidian://open?path=relative.md', 'obsidian://open?path=%2Ftmp%2Fnote.md&append=changed',
    'obsidian://open?path=%2Ftmp%2Fnote.md&x-success=https://example.test',
    'obsidian://open?path=%2Ftmp%2Fnote.md&path=%2Ftmp%2Fother.md',
    'obsidian://open?path=%2Ftmp%2Fnote%00.md',
  ])('does not expose an unusable or destructive target: %s', value => {
    expect(documentPublicationUrl(value)).toBeUndefined();
  });
  it('uses a valid browser URL when the PR URL is invalid', () => {
    expect(documentPublicationTargetUrl({ meta: { pull_request_url: 'javascript:alert(1)', browser_url: 'https://github.com/example/docs/pull/7' } }, 'github'))
      .toBe('https://github.com/example/docs/pull/7');
  });
  it('encodes Windows host paths and does not infer a note from an internal vault ID', () => {
    const value = documentPublicationTargetUrl({ meta: { local_path: 'C:\\My Vault\\中文 #1.md' } }, 'obsidian');
    expect(new URL(value!).searchParams.get('path')).toBe('C:\\My Vault\\中文 #1.md');
    expect(documentPublicationTargetUrl({ uri: 'obsidian://vlt_fixture/note.md' }, 'obsidian')).toBeUndefined();
    expect(documentPublicationTargetUrl({ meta: { local_path: 'relative.md' } }, 'obsidian')).toBeUndefined();
  });
});
