import { beforeEach, describe, expect, it } from 'vitest';
import { useChatInputStore } from './chatInput';

const mention = { mention_id: 'm1', type: 'skill' as const, resource_id: 'test-skill', display_name: '测试技能', start: 0, end: 4 };

describe('chat input drafts', () => {
  beforeEach(() => useChatInputStore.getState().clearAllInputContents());
  it('persists resource metadata with each conversation and clears it after sending', async () => {
    const store = useChatInputStore.getState();
    store.saveInputContent('a', '测试技能 请总结', [mention]);
    store.saveInputContent('b', '另一条消息');
    await useChatInputStore.persist.rehydrate();
    expect(store.getInputMentions('a')).toEqual([mention]);
    expect(store.getInputMentions('b')).toEqual([]);
    store.saveInputContent('a', '测试技能 请总结');
    expect(store.getInputMentions('a')).toEqual([mention]);
    store.clearInputContent('a');
    expect(store.getInputContent('a')).toBe('');
    expect(store.getInputMentions('a')).toEqual([]);
    expect(store.getInputContent('b')).toBe('另一条消息');
  });
});
