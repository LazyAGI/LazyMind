import { useEffect, useState } from 'react';
import { Alert, Button, Empty, Spin } from 'antd';
import { useTranslation } from 'react-i18next';
import ReactMarkdown from 'react-markdown';
import { ChatServiceApi } from '@/modules/chat/utils/request';
import type { ConversationHistoryItem } from '@/api/generated/core-client';

export default function DshConversationHistory({ conversationId, title }: { conversationId: string; title?: string }) {
  const { t } = useTranslation();
  const [history, setHistory] = useState<ConversationHistoryItem[]>([]);
  const [pageToken, setPageToken] = useState('');
  const [nextToken, setNextToken] = useState('');
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading');

  useEffect(() => {
    const controller = new AbortController();
    setState('loading');
    void ChatServiceApi().conversationServiceGetConversationHistory(
      { name: conversationId, pageToken }, { signal: controller.signal },
    ).then(({ data }) => {
      if (controller.signal.aborted) return;
      // The history API returns the newest turn first.
      const earlier = [...(data.history ?? [])].reverse();
      setHistory(current => {
        if (!pageToken) return earlier;
        const visible = new Set(current.map(turn => turn.id));
        return [...earlier.filter(turn => !visible.has(turn.id)), ...current];
      });
      setNextToken(data.next_page_token || '');
      setState('ready');
    }).catch(() => {
      if (!controller.signal.aborted) setState('error');
    });
    return () => controller.abort();
  }, [conversationId, pageToken, retry]);

  return <section className='dsh-conversation-history' aria-label={t('chat.dshHistory')}>
    <header>{title && <h2>{title}</h2>}<span>{t('chat.dshHistory')}</span></header>
    {state === 'error' && <Alert type='error' showIcon message={t('chat.fork.historyLoadFailed')}
      action={<Button onClick={() => setRetry(value => value + 1)}>{t('chat.fork.retryRead')}</Button>} />}
    {state === 'loading' && <Spin />}
    {state === 'ready' && nextToken && <Button onClick={() => setPageToken(nextToken)}>{t('chat.fork.older')}</Button>}
    {state === 'ready' && !history.length && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('chat.dshHistoryEmpty')} />}
    {history.map(turn => <article key={turn.id || turn.seq}>
      {turn.query && <p className='dsh-conversation-query'>{turn.query}</p>}
      {turn.result && <div className='dsh-conversation-answer'><ReactMarkdown>{turn.result}</ReactMarkdown></div>}
    </article>)}
  </section>;
}
