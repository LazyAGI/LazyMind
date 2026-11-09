import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Spin } from 'antd';
import { useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { ChatServiceApi } from '@/modules/chat/utils/request';
import { useConversationUnreadStore } from '@/modules/chat/store/conversationUnread';
import { CHAT_SELECT_CONVERSATION_EVENT } from '@/modules/chat/constants/chat';
import NewChatPage from '../newChat';
import WorkflowRunPage from '../workflowRun';

type ConversationSurface = {
  conversationId: string;
  workflowSessionId?: string;
  title?: string;
  error?: boolean;
};

/** Resolve the execution owner before mounting anything that can submit a local chat. */
export default function ConversationContent() {
  const { conversationId = '' } = useParams<{ conversationId: string }>();
  const { t } = useTranslation();
  const [surface, setSurface] = useState<ConversationSurface>();
  const [retry, setRetry] = useState(0);
  const nativeVisible = useRef(false);
  nativeVisible.current = !conversationId || (surface?.conversationId === conversationId && !surface.error && !surface.workflowSessionId);

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
    ).then(response => {
      if (controller.signal.aborted) return;
      const conversation = response.data.conversation as {
        external_workflow_session_id?: string;
        execution_owner?: string;
        source_type?: string;
        display_name?: string;
      } | undefined;
      if (!conversation) throw new Error('Conversation metadata is unavailable');
      const workflowSessionId = conversation.external_workflow_session_id?.trim();
      if (!workflowSessionId && (conversation.execution_owner === 'external_agent'
        || conversation.source_type === 'external_workflow')) {
        throw new Error('External workflow is unavailable');
      }
      setSurface({ conversationId, workflowSessionId, title: conversation.display_name });
      if (workflowSessionId) useConversationUnreadStore.getState().setCount(conversationId, 0);
    }).catch(() => {
      if (!controller.signal.aborted) setSurface({ conversationId, error: true });
    });
    return () => controller.abort();
  }, [conversationId, retry]);

  if (!conversationId) return <NewChatPage />;
  if (surface?.conversationId !== conversationId) return <div className='conversation-surface-loading'><Spin /></div>;
  if (surface.error) return <Alert type='error' showIcon message={t('chat.fork.historyLoadFailed')}
    action={<Button onClick={() => { setSurface(undefined); setRetry(value => value + 1); }}>{t('chat.fork.retryRead')}</Button>} />;
  if (surface.workflowSessionId) return <WorkflowRunPage key={surface.workflowSessionId}
    sessionId={surface.workflowSessionId} title={surface.title} resultsOnly />;
  return <NewChatPage />;
}
