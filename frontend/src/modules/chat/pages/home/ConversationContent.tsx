import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Spin } from 'antd';
import { useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { ChatServiceApi, WorkflowSessionApi } from '@/modules/chat/utils/request';
import { useConversationUnreadStore } from '@/modules/chat/store/conversationUnread';
import { CHAT_SELECT_CONVERSATION_EVENT, revealChatConversation } from '@/modules/chat/constants/chat';
import NewChatPage from '../newChat';
import DshConversationHistory from './DshConversationHistory';
import WorkflowRunPage from '../workflowRun';

type ConversationSurface = {
  conversationId: string;
  workflowSessionId?: string;
  title?: string;
  dsh?: boolean;
  workflowLookupFailed?: boolean;
  error?: boolean;
};

/** Resolve the execution owner before mounting anything that can submit a local chat. */
export default function ConversationContent() {
  const { conversationId = '' } = useParams<{ conversationId: string }>();
  const { t } = useTranslation();
  const [surface, setSurface] = useState<ConversationSurface>();
  const [retry, setRetry] = useState(0);
  const nativeVisible = useRef(false);
  nativeVisible.current = !conversationId || (surface?.conversationId === conversationId && !surface.error && !surface.workflowSessionId && !surface.dsh);

  useEffect(() => {
    const localConversationCreated = (event: Event) => {
      const detail = (event as CustomEvent<{ conversationId?: string; source?: string }>).detail;
      // A newly submitted native chat changes its route while its stream is
      // already running. Keep that same component mounted across the change.
      if (nativeVisible.current && detail?.source === 'chat' && detail.conversationId) {
        setSurface({ conversationId: detail.conversationId });
      }
    };
    window.addEventListener(CHAT_SELECT_CONVERSATION_EVENT, localConversationCreated);
    return () => window.removeEventListener(CHAT_SELECT_CONVERSATION_EVENT, localConversationCreated);
  }, []);

  useEffect(() => {
    if (!conversationId) return;
    const controller = new AbortController();
    void ChatServiceApi().conversationServiceGetConversationDetail(
      { conversation: conversationId }, { signal: controller.signal },
    ).then(async response => {
      if (controller.signal.aborted) return;
      const conversation = response.data.conversation as {
        external_workflow_session_id?: string;
        execution_owner?: string;
        source_type?: string;
        display_name?: string;
        assistant?: string;
        is_task_conv?: boolean;
      } | undefined;
      if (!conversation) throw new Error('Conversation metadata is unavailable');
      let workflowSessionId = conversation.external_workflow_session_id?.trim();
      if (!workflowSessionId && (conversation.execution_owner === 'external_agent'
        || conversation.source_type === 'external_workflow')) {
        throw new Error('External workflow is unavailable');
      }
      const dsh = !workflowSessionId && conversation.assistant === 'deepseek-harness';
      let workflowLookupFailed = false;
      if (dsh) {
        revealChatConversation(conversation);
        try {
          const { data } = await WorkflowSessionApi().getLatestSession(conversationId, { signal: controller.signal });
          const run = data.data.session;
          if (run?.conversation_id === conversationId && run.controller_host === 'external-agent') {
            workflowSessionId = run.session_id;
          }
        } catch {
          workflowLookupFailed = true;
        }
        if (controller.signal.aborted) return;
      }
      setSurface({ conversationId, workflowSessionId, title: conversation.display_name, dsh, workflowLookupFailed });
      if (workflowSessionId || dsh) useConversationUnreadStore.getState().setCount(conversationId, 0);
    }).catch(() => {
      if (!controller.signal.aborted) setSurface({ conversationId, error: true });
    });
    return () => controller.abort();
  }, [conversationId, retry]);

  if (!conversationId) return <NewChatPage />;
  if (surface?.conversationId !== conversationId) return <div className='conversation-surface-loading'><Spin /></div>;
  if (surface.error) return <Alert type='error' showIcon message={t('chat.fork.historyLoadFailed')}
    action={<Button onClick={() => { setSurface(undefined); setRetry(value => value + 1); }}>{t('chat.fork.retryRead')}</Button>} />;
  const transcript = surface.dsh ? <DshConversationHistory key={conversationId}
    conversationId={conversationId} title={surface.workflowSessionId ? undefined : surface.title} /> : undefined;
  if (surface.workflowSessionId) return <WorkflowRunPage key={surface.workflowSessionId}
    sessionId={surface.workflowSessionId} title={surface.title} resultsOnly conversationContent={transcript} />;
  if (transcript) return <div className='dsh-conversation-view'>
    {surface.workflowLookupFailed && <Alert type='warning' showIcon message={t('chat.workflowRunSyncFailed')}
      action={<Button onClick={() => setRetry(value => value + 1)}>{t('chat.workflowRunRefresh')}</Button>} />}
    {transcript}
  </div>;
  return <NewChatPage />;
}
