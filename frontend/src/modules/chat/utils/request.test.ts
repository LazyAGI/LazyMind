import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ConversationSettingsApi,
  FALLBACK_CHAT_ENTRY_DEFAULTS,
  parseChatEntryDefaults,
  parseThinkingDepth,
  resolveConversationThinkingDepth,
  WorkflowSessionApi,
} from './request';

const { patchMock, postMock } = vi.hoisted(() => ({
  patchMock: vi.fn(),
  postMock: vi.fn(),
}));

vi.mock('@/components/request', () => ({
  axiosInstance: {
    defaults: {},
    patch: patchMock,
    post: postMock,
  },
  BASE_URL: '',
}));

describe('WorkflowSessionApi.saveWriterDocument', () => {
  beforeEach(() => {
    postMock.mockReset();
  });

  it('omits slot when saving the active draft document', () => {
    WorkflowSessionApi().saveWriterDocument(
      'ps-1',
      12,
      '# Draft',
      'draft_document',
      'draft',
    );

    expect(postMock).toHaveBeenCalledWith(
      '/api/core/workflow-sessions/ps-1/writer-document:save',
      { base_revision: 12, document: '# Draft', mode: 'draft' },
      undefined,
    );
  });

  it('keeps the explicit slot when saving an outline document', () => {
    WorkflowSessionApi().saveWriterDocument(
      'ps-1',
      3,
      '# Outline',
      'outline_document',
      'checkpoint',
    );

    expect(postMock).toHaveBeenCalledWith(
      '/api/core/workflow-sessions/ps-1/writer-document:save',
      {
        base_revision: 3,
        document: '# Outline',
        mode: 'checkpoint',
        slot: 'outline_document',
      },
      undefined,
    );
  });
});

describe('chat entry defaults', () => {
  beforeEach(() => {
    patchMock.mockReset();
  });

  it('parses two complete profiles from the API envelope', () => {
    const profiles = {
      quick_question: {
        thinking_depth: 'low',
        conversation_settings: {
          chat_executor: 'lazymind',
          enable_workflow: false,
          workflow_mode: 'auto',
          enable_subagent: false,
        },
      },
      new_task: {
        thinking_depth: 'max',
        conversation_settings: {
          chat_executor: 'codex',
          enable_workflow: true,
          workflow_mode: 'dynamic',
          enable_subagent: true,
        },
      },
    } as const;

    expect(parseChatEntryDefaults({ data: profiles })).toEqual(profiles);
  });

  it('falls back per profile when persisted data is incomplete', () => {
    expect(parseChatEntryDefaults({
      quick_question: { thinking_depth: 'turbo' },
      new_task: FALLBACK_CHAT_ENTRY_DEFAULTS.new_task,
    })).toEqual(FALLBACK_CHAT_ENTRY_DEFAULTS);
  });

  it('derives profiles from legacy flat defaults during a rolling upgrade', () => {
    expect(parseChatEntryDefaults({
      enable_workflow: false,
      workflow_mode: 'auto',
      enable_subagent: false,
    })).toEqual({
      quick_question: {
        thinking_depth: 'medium',
        conversation_settings: {
          chat_executor: 'lazymind',
          enable_workflow: false,
          workflow_mode: 'auto',
          enable_subagent: false,
        },
      },
      new_task: {
        thinking_depth: 'high',
        conversation_settings: {
          chat_executor: 'lazymind',
          enable_workflow: false,
          workflow_mode: 'auto',
          enable_subagent: false,
        },
      },
    });
  });

  it('patches only the selected entry profile', () => {
    const next = FALLBACK_CHAT_ENTRY_DEFAULTS.new_task;
    ConversationSettingsApi().patchChatEntryDefault('new_task', next);

    expect(patchMock).toHaveBeenCalledWith(
      '/api/core/user/chat-settings',
      { new_task: next },
      undefined,
    );
  });

  it('normalizes persisted conversation depth and isolates legacy conversations', () => {
    expect(parseThinkingDepth(' MAX ')).toBe('max');
    expect(parseThinkingDepth('turbo')).toBeUndefined();
    expect(resolveConversationThinkingDepth({ thinking_depth: 'high' })).toBe('high');
    expect(resolveConversationThinkingDepth({})).toBe('medium');
  });
});
