import { CopyOutlined } from '@ant-design/icons';
import { Button, Tooltip, message } from 'antd';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';
import { IdentityAvatar } from '@/modules/identityAvatar';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import '@/modules/chat/components/newChatContainer/index.scss';

function requestText(context: string | undefined, fallback: string | undefined): string {
  if (!context?.trim()) return fallback || '';
  try {
    const value = JSON.parse(context);
    if (typeof value === 'string') return value;
    if (typeof value?.text !== 'string') return fallback || '';
    // Admission appends canonical answers for execution; they are not user prose.
    const marker = '\n\nConfirmed workflow parameters:\n';
    const boundary = value.text.lastIndexOf(marker);
    if (value.clarification_values && boundary >= 0) {
      try {
        const appended = JSON.parse(value.text.slice(boundary + marker.length));
        const answers = value.clarification_values;
        if (Object.keys(appended).length === Object.keys(answers).length
          && Object.keys(appended).every(key => JSON.stringify(appended[key]) === JSON.stringify(answers[key]))) {
          return value.text.slice(0, boundary);
        }
      } catch { /* Preserve unrecognized text verbatim. */ }
    }
    return value.text;
  } catch {
    return context;
  }
}

/** Read-only original request using the same bubble, avatar and toolbar as chat. */
export default function WorkflowRequestMessage({ session, fallback }: { session: WorkflowSession; fallback?: string }) {
  const { t } = useTranslation();
  const text = requestText(session.intent_context, fallback);
  if (!text) return null;
  const time = session.created_at ? dayjs(session.created_at) : undefined;
  return <div className='workflow-request-message chat-chat-container'>
    <div className='chat-box'><div className='chat-item'><div className='user-message-row'>
      {time?.isValid() && <time className='chat-time' dateTime={session.created_at}>{time.format('MM/DD HH:mm')}</time>}
      <div className='user-wrap'>
        <div className='chat-user-bubble-row'>
          <div className='chat-user'><div className='chat-user-mention-text'>{text}</div></div>
          <IdentityAvatar className='chat-user-avatar' kind='profile' size={32} />
        </div>
        <div className='chat-user-toolbar'><Tooltip title={t('chat.copy')}>
          <Button type='text' size='small' aria-label={t('chat.copy')} icon={<CopyOutlined />}
            onClick={() => { void navigator.clipboard.writeText(text).catch(error => message.error(String(error))); }} />
        </Tooltip></div>
      </div>
    </div></div></div>
  </div>;
}
