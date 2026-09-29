import { createInstance } from 'i18next';
import { describe, expect, it } from 'vitest';
import { runStatusDescription } from '@/modules/chat/components/RunStatusCard';
import zhCN from './zh-CN';
import enUS from './en-US';

describe('chat request rejection translation', () => {
  it.each(['zh-CN', 'en-US'])('renders a translated failure in %s', async (lng) => {
    const instance = createInstance();
    await instance.init({ lng, fallbackLng: false, resources: {
      'zh-CN': { translation: zhCN },
      'en-US': { translation: enUS },
    } });
    const key = 'chat.runStatus.codes.request_rejected';
    expect(instance.exists(key)).toBe(true);
    const description = runStatusDescription({
      status: 'failed', reason: 'runtime_failure',
      code: 'request_rejected', partial_output: false,
    }, instance.t);
    expect(description).toContain(instance.t(key));
    expect(description).not.toContain('chat.runStatus.');
  });
});
