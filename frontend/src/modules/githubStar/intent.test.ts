import { describe, expect, it } from 'vitest';
import { getChatConversationPath } from '@/modules/chat/constants/chat';
import { starReturnIntent, starReturnPath } from './intent';

const opportunity = { task_id: 'root-3', conversation_id: 'work-1',
  status: 'succeeded', result_ready: true, source: 'final_result' };

describe('Star authorization return path', () => {
  it.each(['/task-center', '/task-center?tab=tasks', getChatConversationPath('work-1'),
    `${getChatConversationPath('work-1')}?panel=artifacts`])('keeps a supported original page: %s', returnTo => {
    expect(starReturnPath(returnTo)).toBe(returnTo);
    expect(starReturnIntent({ userId: 'owner', returnTo, opportunity }, 'owner'))
      .toEqual({ userId: 'owner', returnTo, opportunity });
  });

  it.each(['https://example.test/steal', '//example.test/steal', '/task-center-other',
    '/agent/chat/home/', '/agent/chat/home/../settings', '/agent/chat/home/work-1/other',
    '/agent/chat/home/work-1\\other', '/settings', null])('rejects an unsupported return: %s', returnTo => {
    expect(starReturnPath(returnTo)).toBeNull();
  });

  it('cannot return an invitation into a different conversation or user', () => {
    expect(starReturnIntent({ userId: 'owner', returnTo: getChatConversationPath('other-work'), opportunity }, 'owner')).toBeNull();
    expect(starReturnIntent({ userId: 'owner', returnTo: getChatConversationPath('work-1'), opportunity }, 'other-owner')).toBeNull();
  });
});
