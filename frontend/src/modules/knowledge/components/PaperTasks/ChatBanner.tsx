import { useEffect, useState } from 'react';
import { Alert, Button, Space, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { axiosInstance, BASE_URL } from '@/components/request';
import { getChatConversationPath, CHAT_CONVERSATION_LIST_REFRESH_EVENT } from '@/modules/chat/constants/chat';
interface Context {
    kind: string;
    expansion_id?: string;
    dataset_id?: string;
    status?: string;
    version: number;
    next_conversation_id?: string;
    source_conversation_id?: string;
    source_available?: boolean;
    mock?: boolean;
    ready_count?: number;
    paper_count?: number;
}
export default function AcademicChatBanner({ conversation }: {
    conversation: string;
}) {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const [params] = useSearchParams();
    const [context, setContext] = useState<Context>();
    const auto = params.get('paper_auto_review') === '1';
    useEffect(() => {
        if (!conversation)
            return;
        let active = true;
        const refresh = async () => {
            try {
                const res = await axiosInstance.get(`${BASE_URL}/api/core/academic/chat-context`, { params: { conversation_id: conversation }, silentError: true } as never);
                if (active)
                    setContext(res.data.data || res.data);
            }
            catch {
            }
        };
        setContext(undefined);
        void refresh();
        const timer = window.setInterval(() => void refresh(), 3000);
        return () => {
            active = false;
            window.clearInterval(timer);
        };
    }, [conversation]);
    useEffect(() => {
        if (auto && context?.next_conversation_id) {
            window.dispatchEvent(new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT));
            navigate(getChatConversationPath(context.next_conversation_id), { replace: true });
        }
    }, [auto, context?.next_conversation_id, navigate]);
    if (!context)
        return null;
    const result = () => navigate(`/lib/knowledge/detail/${encodeURIComponent(context.dataset_id!)}?expansion_id=${encodeURIComponent(context.expansion_id!)}`);
    return <Alert style={{ marginBottom: 8 }} type="info" message={<Space wrap>
        <span>{t(`paperTasks.task.${context.kind}`, { defaultValue: t('paperTasks.resultTitle') })}{context.kind === 'expansion' && ` · ${t(`paperTasks.status.${context.status || 'running'}`)}`}</span>
        {context.mock && <Tag>{t('paperTasks.demo')}</Tag>}
        {context.expansion_id && context.dataset_id && (context.kind === 'expansion' || context.source_available) && <Button size="small" onClick={result}>{t('paperTasks.viewResult')}</Button>}
        {context.source_conversation_id && <Button size="small" onClick={() => navigate(getChatConversationPath(context.source_conversation_id!))}>{t('paperTasks.returnExpansion')}</Button>}
        {context.next_conversation_id && <Button type="primary" size="small" onClick={() => navigate(getChatConversationPath(context.next_conversation_id!))}>{t('paperTasks.viewReview')}</Button>}
        {auto && !context.next_conversation_id && <span>{context.status === 'completed' ? t('paperTasks.reviewNotReady', { count: context.ready_count || 0 }) : t('paperTasks.waitingReview')}</span>}
    </Space>} />;
}
