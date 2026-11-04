import { useTranslation } from 'react-i18next';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import PaperTaskStatus from './PaperTaskStatus';

export default function ReviewRecordStatus({ conversationId }: { conversationId: string }) {
    const { t } = useTranslation();
    const entry = useConversationRunningStore(state => state.entries[conversationId]);
    const status = entry?.status === 'idle' ? entry.terminalStatus : entry?.status;
    const key = status && ({ running: 'chat.conversationRunning', completed: 'chat.conversationCompleted', failed: 'chat.conversationFailed', canceled: 'chat.conversationCanceled', unknown: 'chat.conversationStatusUnavailable' } as const)[status];
    return <PaperTaskStatus status={status}>{t(key || 'paperTasks.recordsMaterialsReceived')}</PaperTaskStatus>;
}
