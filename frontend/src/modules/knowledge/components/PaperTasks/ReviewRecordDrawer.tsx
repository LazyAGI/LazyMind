import { useEffect, useId, useState } from 'react';
import { Alert, Button, Drawer, List, Space, Spin, Typography } from 'antd';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import { getExpansion, getReviewRecordContext, type ExpansionDetail, type ReviewRecordTarget } from '../../api/paperWorkflows';
import ReviewRecordStatus from './ReviewRecordStatus';

export default function ReviewRecordDrawer({ record, onClose, onSource, onChat }: {
    record?: ReviewRecordTarget; onClose: () => void; onSource: (id: string) => void; onChat: (id: string) => void;
}) {
    const { t } = useTranslation();
    const [context, setContext] = useState<Awaited<ReturnType<typeof getReviewRecordContext>>>();
    const [source, setSource] = useState<ExpansionDetail>();
    const [failed, setFailed] = useState(false);
    const [sourceFailed, setSourceFailed] = useState(false);
    const [loading, setLoading] = useState(false);
    const [retry, setRetry] = useState(0);
    const watcher = useId();
    useEffect(() => {
        setContext(undefined); setSource(undefined); setFailed(false); setSourceFailed(false);
        if (!record) return;
        const controller = new AbortController();
        setLoading(true);
        void getReviewRecordContext(record.conversation_id, controller.signal).then(async result => {
            if (controller.signal.aborted) return;
            if (result.kind !== 'review' || !result.input?.papers) throw new Error('Review input unavailable');
            setContext(result); setLoading(false);
            if (result.expansion_id && result.source_available) {
                try {
                    const expansion = await getExpansion(result.expansion_id, true);
                    if (!controller.signal.aborted) setSource(expansion);
                } catch { if (!controller.signal.aborted) setSourceFailed(true); }
            }
        }).catch(() => { if (!controller.signal.aborted) { setFailed(true); setLoading(false); } });
        const store = useConversationRunningStore.getState();
        store.watch(watcher, [record.conversation_id]);
        return () => { controller.abort(); store.unwatch(watcher); };
    }, [record?.conversation_id, retry, watcher]);
    const created = record?.created_at ? dayjs(record.created_at) : undefined;
    return <Drawer className="paper-review-record-drawer" width={780} open={!!record} onClose={onClose} title={t('paperTasks.viewReviewRecord')}>
        {record && <>
            <Typography.Title level={4}>{record.title || t('paperTasks.task.review')}</Typography.Title>
            <Space wrap><ReviewRecordStatus conversationId={record.conversation_id} />{created?.isValid() && <Typography.Text type="secondary">{created.format('YYYY-MM-DD HH:mm')}</Typography.Text>}</Space>
            <div className="paper-review-record-actions"><Button type="primary" onClick={() => onChat(record.conversation_id)}>{t('paperTasks.openContent')}</Button>
                {context?.expansion_id && context.source_available && <Button onClick={() => onSource(context.expansion_id!)}>{t('paperTasks.recordsViewSource')}</Button>}
            </div>
            {loading && <Spin />}
            {failed && <Alert type="error" showIcon message={t('paperTasks.recordsDetailFailed')} action={<Button onClick={() => setRetry(value => value + 1)}>{t('paperTasks.retry')}</Button>} />}
            {context && <>
                <section className="paper-review-record-materials">
                    <Typography.Title level={5}>{t('paperTasks.recordsMaterials')}</Typography.Title>
                    <Space wrap><Typography.Text strong>{t('paperTasks.contentPaperCount', { count: context.input.papers.length })}</Typography.Text><Typography.Text type="secondary">{t('paperTasks.recordsFrozenAtStart')}</Typography.Text></Space>
                    {source && <p className="paper-expansion-muted">{t('paperTasks.recordsExpansionCount', { count: new Set(source.record.result.papers.map(item => item.paper.paper_id)).size })} · {t('paperTasks.recordsCurrentReady', { count: new Set(source.contents.filter(content => content.status === 'ready' && content.content_hash && content.quality?.eligible).map(content => content.paper_id)).size })}</p>}
                    {!context.expansion_id && <p className="paper-expansion-muted">{t('paperTasks.recordsFromLibrary')}</p>}
                    {context.expansion_id && !context.source_available && <p className="paper-expansion-muted">{t('paperTasks.recordsSourceUnavailable')}</p>}
                    {sourceFailed && <Alert type="warning" message={t('paperTasks.recordsSourceFailed')} action={<Button onClick={() => setRetry(value => value + 1)}>{t('paperTasks.retry')}</Button>} />}
                    <p className="paper-expansion-muted">{t('paperTasks.recordsFrozenHint')}</p>
                    <details key={record.id}><summary>{t('paperTasks.recordsViewPapers')}</summary>
                        <List dataSource={context.input.papers} rowKey={item => item.paper.paper_id} renderItem={(item, index) => <List.Item>{index + 1}. {item.paper.title || item.paper.paper_id}</List.Item>} />
                    </details>
                </section>
                <Typography.Title level={5}>{t('paperTasks.recordsInstructions')}</Typography.Title>
                <Typography.Paragraph style={{ whiteSpace: 'pre-wrap' }}>{context.input.user_instruction || t('paperTasks.recordsNoInstruction')}</Typography.Paragraph>
            </>}
        </>}
    </Drawer>;
}
