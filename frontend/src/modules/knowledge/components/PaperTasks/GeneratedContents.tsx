import { useEffect, useId, useState } from 'react';
import { Button, List, Space } from 'antd';
import { ReadOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';
import { axiosInstance, BASE_URL } from '@/components/request';
import type { AcademicChatContext, ConversationDetailItem } from '@/api/generated/core-client';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import type { ExpansionDetail, ReviewRecordTarget } from '../../api/paperWorkflows';

import ReviewRecordStatus from './ReviewRecordStatus';

type Handoff = ExpansionDetail['handoffs'][number];

function ContentRow({ item, number, onOpen, onReview }: { item: Handoff; number: number; onOpen: (id: string) => void; onReview?: (record: ReviewRecordTarget) => void }) {
    const { t } = useTranslation();
    const [conversation, setConversation] = useState<ConversationDetailItem>();
    const [paperCount, setPaperCount] = useState<number>();
    useEffect(() => {
        const controller = new AbortController();
        const options = { signal: controller.signal, silentError: true } as never;
        void axiosInstance.get(`${BASE_URL}/api/core/conversations/${encodeURIComponent(item.conversation_id)}`, options).then(response => {
            if (!controller.signal.aborted) setConversation(response.data.data || response.data);
        }).catch(() => {});
        void axiosInstance.get(`${BASE_URL}/api/core/academic/chat-context`, { signal: controller.signal, silentError: true, params: { conversation_id: item.conversation_id } } as never).then(response => {
            const context: AcademicChatContext = response.data.data || response.data;
            if (!controller.signal.aborted && context.input?.papers) setPaperCount(context.input.papers.length);
        }).catch(() => {});
        return () => controller.abort();
    }, [item.conversation_id]);
    const created = conversation?.create_time ? dayjs(conversation.create_time) : undefined;
    return <List.Item actions={[...(item.kind === 'review' && onReview ? [<Button type="link" key="record" onClick={() => onReview({ ...item, title: conversation?.display_name, created_at: conversation?.create_time })}>{t('paperTasks.viewReviewRecord')}</Button>] : []), <Button type="link" key="open" onClick={() => onOpen(item.conversation_id)}>{t('paperTasks.openContent')}</Button>]}>
        <List.Item.Meta title={conversation?.display_name || `${t(`paperTasks.task.${item.kind}`, { defaultValue: item.kind })} ${number}`}
            description={<Space wrap size={[12, 4]}>
                {paperCount !== undefined && <span>{t('paperTasks.contentPaperCount', { count: paperCount })}</span>}
                {created?.isValid() && <time dateTime={conversation?.create_time}>{created.format('MM-DD HH:mm')}</time>}
                <ReviewRecordStatus conversationId={item.conversation_id} />
            </Space>} />
    </List.Item>;
}

export default function GeneratedContents({ items, onOpen, onReview }: { items: Handoff[]; onOpen: (id: string) => void; onReview?: (record: ReviewRecordTarget) => void }) {
    const { t } = useTranslation();
    const [open, setOpen] = useState(false);
    const watcher = useId();
    const ids = items.map(item => item.conversation_id).join(',');
    useEffect(() => {
        const store = useConversationRunningStore.getState();
        store.watch(watcher, open ? ids.split(',').filter(Boolean) : []);
        return () => store.unwatch(watcher);
    }, [watcher, ids, open]);
    if (!items.length) return null;
    return <details className="paper-expansion-generated" onToggle={event => setOpen(event.currentTarget.open)}>
        <summary><ReadOutlined aria-hidden /><span>{t(items.every(item => item.kind === 'review') ? 'paperTasks.task.review' : 'paperTasks.generated')}</span><span className="paper-expansion-muted">（{items.length}）</span></summary>
        {open && <List dataSource={items.map((item, index) => ({ item, number: index + 1 })).reverse()}
            renderItem={({ item, number }) => <ContentRow key={item.id} item={item} number={number} onOpen={onOpen} onReview={onReview} />} />}
    </details>;
}
