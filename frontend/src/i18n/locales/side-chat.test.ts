import { createInstance } from 'i18next';
import { describe, expect, it } from 'vitest';
import zhCN from './zh-CN';
import enUS from './en-US';

describe('side chat menu translation', () => {
  it.each([
    ['zh-CN', '展开侧聊'],
    ['en-US', 'Open side chat'],
  ])('resolves the menu label in %s instead of exposing its key', async (lng, expected) => {
    const instance = createInstance();
    await instance.init({ lng, fallbackLng: false, resources: {
      'zh-CN': { translation: zhCN },
      'en-US': { translation: enUS },
    } });
    expect(instance.t('chat.sideChat.openPanel')).toBe(expected);
  });
});
