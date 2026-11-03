import { useId, useState, type ReactNode } from 'react';
import { Button, Tag, Typography } from 'antd';
import { BranchesOutlined, CheckOutlined, DownOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { ExpansionDetail } from '../../api/paperWorkflows';
import MarkdownViewer from '../MarkdownViewer';
import { PaperExpansionArtifactView } from '@/modules/chat/components/WorkflowPanel/PaperExpansionArtifactView';
import PreparationPanel, { expansionProgressStages } from './PreparationPanel';
import PaperTaskStatus from './PaperTaskStatus';

export default function ExpansionDetails({ detail, statusLabel, onRefresh, onStart, children }: {
    children?: ReactNode; detail: ExpansionDetail; statusLabel: (status: string) => string; onRefresh: () => Promise<void>; onStart: (id: string) => void;
}) {
    const { t } = useTranslation();
    const [processOpen, setProcessOpen] = useState(false);
    const processId = useId();
    const { record } = detail;
    const report = record.report;
    const candidate = record.progress?.screened_candidates ?? record.progress?.candidate_set;
    const publishedList = record.goal === 'expansion' && candidate?.list_mode === 'deep' ? candidate : undefined;
    const completed = record.status === 'completed';
    const preparing = ['preparing', 'awaiting_info', 'awaiting_confirmation', 'preparation_failed'].includes(record.status);
    const running = ['running', 'active'].includes(record.status);
    const originalInstruction = record.preparation?.original_instruction || record.result.user_instruction;
    const directions = record.result.selected_directions?.filter(direction => direction.trim() !== originalInstruction.trim()) || [];
    const seeds = record.result.papers.filter(item => item.is_seed);
    // A published intermediate artifact is not the completion of the whole expansion.
    const expansionStages = expansionProgressStages(record);
    const stageDone = (index: number) => completed || (index < expansionStages.length - 1 && !!record.progress?.[expansionStages[index]]);
    const currentStage = expansionStages.findIndex((_, index) => !stageDone(index));
    const stageTitle = (slot: string) => t(`paperTasks.stage.${record.goal === 'expansion' && record.budget_mode === 'deep' && slot === 'relation_analysis' ? 'deep_analysis' : slot}`);
    const completedStages = expansionStages.filter((_, index) => stageDone(index)).length;
    return <>
        <section className="paper-expansion-context" aria-label={t('paperTasks.overview.context')}>
            {originalInstruction && <div className="paper-expansion-original-request">
                <Typography.Text type="secondary">{t('paperTasks.originalRequest')}</Typography.Text>
                <Typography.Paragraph ellipsis={{ rows: 2, expandable: 'collapsible', symbol: (expanded: boolean) => t(expanded ? 'paperTasks.overview.collapse' : 'paperTasks.overview.expand') }}>{originalInstruction}</Typography.Paragraph>
            </div>}
            {!!seeds.length && <div className="paper-expansion-context-seeds">
                <Tag color="purple" bordered={false} icon={<BranchesOutlined />}>{t('paperTasks.paperAnalysis.seed')}</Tag>
                <Typography.Paragraph ellipsis={{ rows: 2, expandable: 'collapsible', symbol: (expanded: boolean) => t(expanded ? 'paperTasks.overview.collapse' : 'paperTasks.overview.expand') }}>{seeds.map(item => item.paper.title || item.paper.paper_id).join('；')}</Typography.Paragraph>
            </div>}
            {!record.preparation && !!directions.length && <div className="paper-expansion-context-directions">
                <Typography.Text type="secondary">{t('paperTasks.overview.confirmedDirections')}</Typography.Text>
                {directions.map(direction => <Typography.Paragraph key={direction}>{direction}</Typography.Paragraph>)}
            </div>}
            {!record.preparation?.skipped && <PreparationPanel embedded detail={detail} onRefresh={onRefresh} onStart={onStart} />}
            {record.result.user_instruction && record.result.user_instruction !== originalInstruction && <details className="paper-expansion-current-request">
                <summary>{t('paperTasks.currentRequest')}</summary>
                <Typography.Paragraph>{record.result.user_instruction}</Typography.Paragraph>
            </details>}
        </section>
        {preparing ? children : <section className="paper-expansion-progress" aria-label={t('paperTasks.overview.progress')}>
            <div className="paper-expansion-progress-heading">
                <Typography.Text strong>{t('paperTasks.overview.progress')}</Typography.Text>
                <PaperTaskStatus status={record.status}>{statusLabel(record.status)}{running ? ` · ${completedStages} / ${expansionStages.length}` : ''}</PaperTaskStatus>
                <Button type="link" size="small" aria-expanded={processOpen} aria-controls={processId} onClick={() => setProcessOpen(open => !open)}>
                    {t(processOpen ? 'paperTasks.overview.hideProcess' : 'paperTasks.overview.showProcess')}<DownOutlined aria-hidden rotate={processOpen ? 180 : 0} />
                </Button>
            </div>
            <Typography.Paragraph className="paper-expansion-progress-current">{completed ? t(record.report?.list_mode ? 'paperTasks.paperListReady' : 'paperTasks.overview.completed')
                : running ? t('paperTasks.overview.working', { stage: stageTitle(expansionStages[currentStage]) })
                    : record.status === 'pending' ? t('paperTasks.overview.waiting') : t('paperTasks.overview.interrupted')}</Typography.Paragraph>
            {!completed && !publishedList && <Typography.Text type="secondary">{t('paperTasks.overview.resultsAfterCompletion')}</Typography.Text>}
            <div id={processId} hidden={!processOpen}>
                {children}
                <ol className="paper-expansion-timeline">{expansionStages.map((slot, index) => {
                    const done = stageDone(index);
                    const active = running && index === currentStage;
                    const artifact = record.progress?.[slot];
                    const input = artifact?.input || artifact;
                    const count = new Set(input?.papers?.map(item => item.paper.paper_id)).size;
                    return <li key={slot} data-status={done ? 'done' : active ? 'running' : 'pending'}>
                        <span className="paper-expansion-stage-marker">{done ? <CheckOutlined /> : index + 1}</span>
                        <div className="paper-expansion-stage-content">
                            <Typography.Text strong>{stageTitle(slot)}</Typography.Text>
                            <p className="paper-expansion-muted">{done ? t('paperTasks.status.completed') : active ? t('paperTasks.status.running') : t('paperTasks.overview.stageWaiting')}
                                {!!count && <> · {t('paperTasks.overview.processedPapers', { count })}</>}</p>

                        </div>
                    </li>;
                })}</ol>
            </div>
        </section>}
        {!completed && publishedList && <section className="paper-expansion-section paper-expansion-published-list" aria-label={t('paperTasks.paperListReady')}>
            <Typography.Text strong>{t('paperTasks.paperListReady')}</Typography.Text>
            <PaperExpansionArtifactView slotId="candidate_set" data={publishedList} />
        </section>}
        {completed && report?.narrative && <details className="paper-expansion-section">
            <summary>{t('paperTasks.researchReading')}<span>{t('paperTasks.researchReadingHint')}</span></summary>
            <section className="paper-expansion-reading" aria-label={t('paperTasks.researchReadingHint')}>
                <MarkdownViewer>{report.narrative}</MarkdownViewer>

            </section>
        </details>}
    </>;
}
