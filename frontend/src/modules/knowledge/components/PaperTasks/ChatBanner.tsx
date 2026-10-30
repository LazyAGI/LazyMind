import { useEffect, useState } from 'react';
import { Alert, Button, Space } from 'antd';
import { isAxiosError } from 'axios';
import { useTranslation } from 'react-i18next';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { axiosInstance, BASE_URL } from '@/components/request';
import { getChatConversationPath, CHAT_CONVERSATION_LIST_REFRESH_EVENT } from '@/modules/chat/constants/chat';
import type { AcademicChatSummary } from '@/api/generated/core-client';
import ReviewFlow from './ReviewFlow';
export default function AcademicChatBanner({ conversation }: {
    conversation: string;
}) {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const [params] = useSearchParams();
    const [context, setContext] = useState<AcademicChatSummary>();
    const auto = params.get('paper_auto_review') === '1';
    useEffect(() => {
        if (!conversation)
            return;
        let active = true;
        let pending = false;
        let timer: number | undefined;
        const schedule = () => {
            window.clearTimeout(timer);
            timer = window.setTimeout(() => void refresh(), 3000);
        };
        const refresh = async () => {
            if (!active || pending)
                return;
            window.clearTimeout(timer);
            pending = true;
            try {
                const res = await axiosInstance.get(`${BASE_URL}/api/core/academic/chat-context`, { params: { conversation_id: conversation, view: 'summary' }, silentError: true } as never);
                if (!active)
                    return;
                const next: AcademicChatSummary = res.data.data || res.data;
                setContext(next);
                if (next.kind === 'expansion' && !next.next_conversation_id && (
                    !['completed', 'failed', 'canceled'].includes(next.status || '') || next.goal === 'review' && next.status === 'completed'
                ))
                    schedule();
            }
            catch (error) {
                if (active && !(isAxiosError(error) && [403, 404].includes(error.response?.status || 0)))
                    schedule();
            }
            finally {
                pending = false;
            }
        };
        const onFocus = () => void refresh();
        setContext(undefined);
        void refresh();
        window.addEventListener('focus', onFocus);
        return () => {
            active = false;
            window.clearTimeout(timer);
            window.removeEventListener('focus', onFocus);
        };
    }, [conversation, auto]);
    useEffect(() => {
        if (auto && context?.goal === 'review' && context.next_conversation_id) {
            window.dispatchEvent(new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT));
            navigate(getChatConversationPath(context.next_conversation_id), { replace: true });
        }
    }, [auto, context?.goal, context?.next_conversation_id, navigate]);
    if (!context)
        return null;
    const review = context.goal === 'review' || context.kind === 'review';
    const result = () => navigate(`/lib/knowledge/detail/${encodeURIComponent(context.dataset_id!)}?expansion_id=${encodeURIComponent(context.expansion_id!)}`);
    const actions = <Space wrap>
        {context.expansion_id && context.dataset_id && (context.kind === 'expansion' || context.source_available) && <Button size="small" onClick={result}>{t(review ? 'paperTasks.reviewFlow.viewMaterials' : 'paperTasks.viewResult')}</Button>}
        {context.kind !== 'review' && context.source_conversation_id && <Button size="small" onClick={() => navigate(getChatConversationPath(context.source_conversation_id!))}>{t('paperTasks.returnExpansion')}</Button>}
        {context.next_conversation_id && <Button type="primary" size="small" onClick={() => navigate(getChatConversationPath(context.next_conversation_id!))}>{t('paperTasks.viewReview')}</Button>}
    </Space>;
    if (review) return <div>
        <strong>{t(context.kind === 'review' ? 'paperTasks.task.review' : 'paperTasks.reviewFlow.preparationTitle')}</strong>
        <ReviewFlow status={context.status} reviewStarted={context.kind === 'review' || !!context.next_conversation_id} readyCount={context.ready_count}>
            {actions}
        </ReviewFlow>
    </div>;
    return <Alert style={{ marginBottom: 8 }} type="info" message={<Space wrap>
        <span>{t(`paperTasks.task.${context.kind}`, { defaultValue: t('paperTasks.resultTitle') })}{context.kind === 'expansion' && ` · ${t(`paperTasks.status.${context.status || 'running'}`)}`}</span>
        {actions}
    </Space>} />;
}
