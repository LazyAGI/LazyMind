import { useTranslation } from 'react-i18next';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import PaperTaskStatus from './PaperTaskStatus';

export default function ReviewRecordStatus({ conversationId, kind = 'review' }: { conversationId: string; kind?: string }) {
    const { t } = useTranslation();
    const entry = useConversationRunningStore(state => state.entries[conversationId]);
    const status = entry?.status === 'idle' ? entry.terminalStatus : entry?.status;
    const key = status && ({ running: 'chat.conversationRunning', completed: 'chat.conversationCompleted', failed: 'chat.conversationFailed', canceled: 'chat.conversationCanceled', unknown: 'chat.conversationStatusUnavailable' } as const)[status];
    const label = t(key || 'paperTasks.recordsMaterialsReceived');
    return <PaperTaskStatus status={status}>
        {key && kind === 'review' ? t('paperTasks.reviewFlow.conversationStatus', { status: label }) : label}
    </PaperTaskStatus>;
}
