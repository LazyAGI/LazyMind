import { describe, expect, it } from 'vitest';
import i18n from '@/i18n';
import { documentPublicationErrorMessage } from './documentPublicationError';

describe('publication failure stages', () => {
  it.each(['feishu', 'notion', 'github', 'wechat', 'obsidian', 'custom-provider'])(
    'identifies conversion failures before writing to %s', async (provider) => {
      await i18n.changeLanguage('zh-CN');
      const message = documentPublicationErrorMessage('DOCUMENT_CONVERSION_FAILED', provider);
      expect(message).toContain('文档转换阶段失败');
      expect(message).toContain('尚未写入');
      expect(message).not.toContain('chat.writerIR.');
    },
  );
  it('distinguishes preparation, provider lookup, authorization, and local persistence', async () => {
    await i18n.changeLanguage('zh-CN');
    expect(documentPublicationErrorMessage('ARTIFACT_IN_USE')).toContain('内容检查阶段');
    expect(documentPublicationErrorMessage('DOCUMENT_PROVIDERS_UNAVAILABLE')).toContain('数据源加载阶段');
    expect(documentPublicationErrorMessage('PROVIDER_CREDENTIALS_UNAVAILABLE')).toContain('授权检查阶段');
    const message = documentPublicationErrorMessage('PROVIDER_SYNC_LOCAL_PERSIST_FAILED', 'feishu');
    expect(message).toContain('本地结果保存失败');
    expect(message).toContain('已写入飞书');
    expect(message).toContain('不要重复写入');
  });
  it.each([undefined, 'PUBLICATION_OUTCOME_UNKNOWN', 'UNRECOGNIZED_CODE'])(
    'does not encourage blind retries for uncertain outcomes (%s)', async (code) => {
      await i18n.changeLanguage('zh-CN');
      const message = documentPublicationErrorMessage(code, 'notion');
      expect(message).toContain('结果待确认');
      expect(message).toContain('不要直接重试');
      expect(message).not.toContain('尚未写入');
    },
  );
  it('provides English phase guidance', async () => {
    await i18n.changeLanguage('en-US');
    expect(documentPublicationErrorMessage('DOCUMENT_CONVERSION_FAILED', 'github')).toContain('Document conversion failed');
  });
  it('explains the chat-use switch and an earlier operation blocking this write', async () => {
    await i18n.changeLanguage('zh-CN');
    expect(documentPublicationErrorMessage('PROVIDER_CREDENTIALS_UNAVAILABLE','feishu')).toContain('对话使用');
    const pending=documentPublicationErrorMessage('PUBLICATION_IN_PROGRESS','feishu');
    expect(pending).toContain('上次');
    expect(pending).toContain('当前写入已阻止');
    expect(pending).not.toContain('尚未写入');
  });
});
