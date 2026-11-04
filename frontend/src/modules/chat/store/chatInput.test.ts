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


it('persists repair attachments with their exact draft and removes the recovery after submission', async () => {
 const files = [{ id: 'upload:one', name: 'one.pdf', input: { input_type: 'file' as const, uri: '/files/one', filename: 'one.pdf' } }];
 const store = useChatInputStore.getState();
 store.saveWorkflowRepairDraft('repair', { text: '测试技能 draft', mentions: [mention], files, runInBackground: true });
 await useChatInputStore.persist.rehydrate();
 expect(useChatInputStore.getState().workflowRepairDrafts.repair).toMatchObject({ files, runInBackground: true, mentions: [mention] });
 store.saveInputContent('repair', '测试技能 updated', [mention]);
 expect(useChatInputStore.getState().workflowRepairDrafts.repair.text).toBe('测试技能 updated');
 store.clearInputContent('repair');
 expect(useChatInputStore.getState().workflowRepairDrafts.repair).toBeUndefined();
});
