import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Empty, Spin } from 'antd';
import { useTranslation } from 'react-i18next';
import ReactMarkdown from 'react-markdown';
import { ChatServiceApi, WorkflowSessionApi } from '@/modules/chat/utils/request';
import type { ConversationHistoryItem } from '@/api/generated/core-client';
import WorkflowRunPage from '../workflowRun';

export default function DshConversationHistory({ conversationId, title }: { conversationId: string; title?: string }) {
  const { t } = useTranslation();
  const [history, setHistory] = useState<ConversationHistoryItem[]>([]);
  const [request, setRequest] = useState({ pageToken: '', pages: 1 });
  const loadedPages = useRef(0);
  const [nextToken, setNextToken] = useState('');
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading');
  const [workflowSessionId, setWorkflowSessionId] = useState<string>();
  const [lookupFailed, setLookupFailed] = useState(false);
  const [lookupRevision, setLookupRevision] = useState(0);

  useEffect(() => {
    // A tab switch can emit both events; re-read the same open window of turns once.
    let timer: ReturnType<typeof setTimeout>;
    const refresh = () => {
      clearTimeout(timer);
      if (document.hidden) return;
      timer = setTimeout(() => {
        setRequest({ pageToken: '', pages: Math.max(1, loadedPages.current) });
        setLookupRevision(value => value + 1);
      }, 100);
    };
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      clearTimeout(timer);
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void WorkflowSessionApi().getLatestSession(conversationId, { signal: controller.signal }).then(({ data }) => {
      if (controller.signal.aborted) return;
      const run = data.data.session;
      setWorkflowSessionId(run?.conversation_id === conversationId && run.controller_host === 'external-agent' ? run.session_id : undefined);
      setLookupFailed(false);
    }).catch(() => {
      if (!controller.signal.aborted) setLookupFailed(true);
    });
    return () => controller.abort();
  }, [conversationId, lookupRevision]);

  useEffect(() => {
    const controller = new AbortController();
    setState('loading');
    const load = async () => {
      const turns: ConversationHistoryItem[] = [];
      let token = request.pageToken;
      let count = 0;
      do {
        const { data } = await ChatServiceApi().conversationServiceGetConversationHistory(
          { name: conversationId, pageToken: token }, { signal: controller.signal },
        );
        if (controller.signal.aborted) return;
        turns.push(...(data.history ?? []));
        token = data.next_page_token || '';
        count++;
      } while (token && count < request.pages);
      // The API pages newest first. Keep one copy at overlapping page boundaries.
      setHistory(current => {
        const seen = new Set<string | number | undefined>();
        const newestFirst = request.pageToken ? [...current].reverse().concat(turns) : turns;
        return newestFirst.filter(turn => {
          const key = turn.id || turn.seq;
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        }).reverse();
      });
      loadedPages.current = (request.pageToken ? loadedPages.current : 0) + count;
      setNextToken(token);
      setState('ready');
    };
    void load().catch(() => {
      if (!controller.signal.aborted) setState('error');
    });
    return () => controller.abort();
  }, [conversationId, request]);

  const transcript = <section className='dsh-conversation-history' aria-label={t('chat.dshHistory')}>
    <header>{title && !workflowSessionId && <h2>{title}</h2>}<span>{t('chat.dshHistory')}</span></header>
    {lookupFailed && <Alert type='warning' showIcon message={t('chat.workflowRunSyncFailed')}
      action={<Button onClick={() => setLookupRevision(value => value + 1)}>{t('chat.workflowRunRefresh')}</Button>} />}
    {state === 'error' && <Alert type='error' showIcon message={t('chat.fork.historyLoadFailed')}
      action={<Button onClick={() => setRequest({ ...request })}>{t('chat.fork.retryRead')}</Button>} />}
    {state === 'loading' && !history.length && <Spin />}
    {state === 'ready' && nextToken && <Button onClick={() => setRequest({ pageToken: nextToken, pages: 1 })}>{t('chat.fork.older')}</Button>}
    {state === 'ready' && !history.length && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('chat.dshHistoryEmpty')} />}
    {history.map(turn => <article key={turn.id || turn.seq}>
      {turn.query && <p className='dsh-conversation-query'>{turn.query}</p>}
      {turn.result && <div className='dsh-conversation-answer'><ReactMarkdown>{turn.result}</ReactMarkdown></div>}
    </article>)}
  </section>;
  if (workflowSessionId) return <WorkflowRunPage key={workflowSessionId} sessionId={workflowSessionId}
    title={title} resultsOnly conversationContent={transcript} />;
  return <div className='dsh-conversation-view'>{transcript}</div>;
}
