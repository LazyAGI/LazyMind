import { useEffect, useId, useState } from 'react';
import { Alert, List, Tabs, Tooltip } from 'antd';
import { ArrowRightOutlined, BranchesOutlined, ReadOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import type { Expansion, ReviewRecord } from '../../api/paperWorkflows';
import ReviewRecordStatus from './ReviewRecordStatus';
import PaperTaskStatus from './PaperTaskStatus';

export default function ResearchRecords({ expansions, reviews, open, loading, error, statusLabel, onExpansion, onReview }: {
    expansions: Expansion[]; reviews: ReviewRecord[]; open: boolean; loading: boolean; error: boolean;
    statusLabel: (status: string) => string; onExpansion: (id: string) => void; onReview: (record: ReviewRecord) => void;
}) {
    const { t } = useTranslation();
    const [filter, setFilter] = useState('all');
    const watcher = useId();
    const ids = reviews.map(review => review.conversation_id).join(',');
    useEffect(() => {
        const store = useConversationRunningStore.getState();
        store.watch(watcher, open && filter !== 'expansion' ? ids.split(',').filter(Boolean) : []);
        return () => store.unwatch(watcher);
    }, [watcher, ids, open, filter]);
    const rows = [
        ...expansions.map(record => ({ kind: 'expansion' as const, record })),
        ...reviews.map(record => ({ kind: 'review' as const, record })),
    ].filter(row => filter === 'all' || row.kind === filter).sort((a, b) => (Date.parse(b.record.updated_at) || 0) - (Date.parse(a.record.updated_at) || 0) || a.record.id.localeCompare(b.record.id));
    return <>
        <Tabs activeKey={filter} onChange={setFilter} items={[
            { key: 'all', label: `${t('paperTasks.recordsAll')} ${expansions.length + reviews.length}` },
            { key: 'expansion', label: `${t('paperTasks.recordsExpansion')} ${expansions.length}` },
            { key: 'review', label: `${t('paperTasks.task.review')} ${reviews.length}` },
        ]} />
        {error && <Alert type="error" showIcon message={t('paperTasks.recordsLoadFailed')} />}
        <p className="paper-expansion-muted">{t('paperTasks.recordsSort')}</p>
        <List className="paper-expansion-history-list" rowKey={row => `${row.kind}:${row.record.id}`} dataSource={rows} loading={loading}
            locale={{ emptyText: error ? t('paperTasks.recordsLoadFailed') : t('paperTasks.noRecords') }} renderItem={row => {
                const { record } = row;
                const updated = dayjs(record.updated_at);
                const related = reviews.filter(review => review.expansion_id === record.id).length;
                return <List.Item><button type="button" className="paper-expansion-history-row paper-research-record" aria-label={`${t(row.kind === 'review' ? 'paperTasks.viewReviewRecord' : 'paperTasks.viewResult')}: ${record.title}`}
                    onClick={() => row.kind === 'review' ? onReview(row.record) : onExpansion(record.id)}>
                    <span className="paper-expansion-history-content">
                        <span className="paper-research-record-kind">{row.kind === 'review' ? <ReadOutlined /> : <BranchesOutlined />}{t(row.kind === 'expansion' ? 'paperTasks.recordsExpansion' : 'paperTasks.task.review')}
                            <span className="paper-research-record-status">{row.kind === 'review' ? <ReviewRecordStatus conversationId={record.conversation_id} /> : <PaperTaskStatus status={row.record.status}>{statusLabel(row.record.status)}</PaperTaskStatus>}</span>
                        </span>
                        <Tooltip title={record.title} placement="topLeft"><span className="paper-expansion-history-title">{record.title}</span></Tooltip>
                        <span className="paper-expansion-history-meta">{row.kind === 'review' ? <>
                            <span>{t('paperTasks.contentPaperCount', { count: row.record.paper_count })}</span>
                            <span>{t(row.record.expansion_id ? 'paperTasks.recordsFromExpansion' : 'paperTasks.recordsFromLibrary')}</span>
                        </> : <>
                            {row.record.status === 'completed' && <span>{t('paperTasks.recordsExpansionCount', { count: new Set(row.record.result?.papers.map(item => item.paper.paper_id) || []).size })}</span>}
                            {related > 0 && <span>{t('paperTasks.recordsRelatedCount', { count: related })}</span>}
                        </>}</span>
                        {row.kind === 'review' && row.record.expansion_id && <span className="paper-expansion-muted">{t('paperTasks.recordsSource', { title: row.record.source_title || t('paperTasks.recordsSourceUnavailable') })}</span>}
                        {updated.isValid() && <time className="paper-expansion-muted" dateTime={record.updated_at}>{updated.format('MM-DD HH:mm')}</time>}
                    </span>
                    <span className="paper-expansion-history-view" aria-hidden>{t('common.view')}<ArrowRightOutlined /></span>
                </button></List.Item>;
            }} />
    </>;
}
