import { useEffect, useId, useState } from 'react';
import { Button, Tag, Typography } from 'antd';
import { BranchesOutlined, DownOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { ExpansionDetail } from '../../api/paperWorkflows';
import MarkdownViewer from '../MarkdownViewer';
import PreparationPanel from './PreparationPanel';
import PaperFlow from './PaperFlow';
import { paperDetailFlow, paperExpansionDetailFlow, reviewExpansionDetailFlow, reviewGenerationStatus, reviewMaterialCounts, stageArtifacts } from './paperFlowModel';
import ExpansionSteps from './ExpansionSteps';
import ReviewRecordStatus from './ReviewRecordStatus';
import { useConversationRunningStore } from '@/modules/chat/store/conversationRunning';
import PaperResearchCompletion from '@/modules/chat/components/WorkflowPanel/PaperResearchCompletion';
import PaperLineageView from '@/modules/chat/components/WorkflowPanel/PaperLineageView';
import { PaperDetail } from '@/modules/chat/components/WorkflowPanel/PaperExpansionArtifactView';

export default function ExpansionDetails({ detail, onRefresh, onStart }: {
    detail: ExpansionDetail; onRefresh: () => Promise<void>; onStart: (id: string) => void;
}) {
    const { t } = useTranslation();
    const [processOpen, setProcessOpen] = useState(false);
    const processId = useId();
    const { record } = detail;
    const watcher = useId();
    const reviewConversationId = detail.handoffs.find(item => item.id === record.auto_review_id && item.kind === 'review')?.conversation_id;
    const execution = useConversationRunningStore(state => reviewConversationId ? state.entries[reviewConversationId] : undefined);
    useEffect(() => {
        if (!reviewConversationId) return;
        const store = useConversationRunningStore.getState();
        store.watch(watcher, [reviewConversationId]);
        return () => store.unwatch(watcher);
    }, [watcher, reviewConversationId]);
    const report = record.report;
    const completed = record.status === 'completed';
    const preparing = ['preparing', 'awaiting_info', 'awaiting_confirmation', 'preparation_failed'].includes(record.status);
    const review = record.goal === 'review';
    const originalInstruction = record.preparation?.original_instruction || record.result.user_instruction;
    const directions = record.result.selected_directions?.filter(direction => direction.trim() !== originalInstruction.trim()) || [];
    const seeds = record.result.papers.filter(item => item.is_seed);
    const steps = paperDetailFlow(detail, reviewGenerationStatus(execution));
    const expansionSteps = paperExpansionDetailFlow(detail);
    const fulltext = expansionSteps.find(step => step.id === 'prepareFulltext');
    const { ready, distinctReady, processing } = reviewMaterialCounts(detail);
    const processCounts = expansionSteps.flatMap(step => {
        const artifact = record.progress?.[stageArtifacts[step.id]];
        const papers = (artifact?.input || artifact)?.papers;
        return papers?.length ? [{ step, count: new Set(papers.map(paper => paper.paper.paper_id)).size }] : [];
    });
    return <>
        <PaperFlow title={t(review ? 'paperTasks.reviewFlow.label' : 'paperTasks.overview.progress')} steps={steps}>
            <div className="paper-task-flow-summary">
                {review && record.auto_review_id ? <ReviewRecordStatus conversationId={reviewConversationId || ''} />
                    : completed && report?.completion && <PaperResearchCompletion completion={report.completion} />}
                {fulltext?.status === 'needFulltext' && <Typography.Text type="warning">
                    {t('paperTasks.reviewFlow.needFulltextHint', { count: distinctReady })}
                </Typography.Text>}
                {review && record.report?.research_complete === false && !record.auto_review_id && <Typography.Text type="warning">{t('paperTasks.reviewFlow.incompleteResearchHint')}</Typography.Text>}
                {completed && fulltext?.status !== 'needFulltext' && detail.contents.length > 0 && <Typography.Text type="secondary">
                    {t('paperTasks.availableCount', { count: ready })}{fulltext?.status === 'running' && <> · {t('paperTasks.processingFulltextCount', { count: processing })}</>}
                </Typography.Text>}
                {!completed && !preparing && <Typography.Text type="secondary">{t('paperTasks.overview.resultsAfterCompletion')}</Typography.Text>}
                {!!processCounts.length && <Button type="link" size="small" aria-expanded={processOpen} aria-controls={processId} onClick={() => setProcessOpen(open => !open)}>
                    {t(processOpen ? 'paperTasks.overview.hideProcess' : 'paperTasks.overview.showProcess')}<DownOutlined aria-hidden rotate={processOpen ? 180 : 0} />
                </Button>}
            </div>
            <ul className="paper-task-flow-counts" id={processId} hidden={!processOpen}>
                {processCounts.map(({ step, count }) => <li key={step.id}>
                    <strong>{step.labelKey ? t(step.labelKey) : step.label || step.id}</strong>
                    <span>{t('paperTasks.overview.processedPapers', { count })}</span>
                </li>)}
            </ul>
        </PaperFlow>
        {review && <section className="paper-review-expansion-progress" aria-label={t(`paperTasks.expansionDepth.${record.budget_mode}.label`)}>
            <Typography.Title level={5}>{t(`paperTasks.expansionDepth.${record.budget_mode}.label`)}</Typography.Title>
            {record.budget_mode !== 'fast' && record.preparation?.direction_selection && <p className="paper-expansion-muted">{t('paperTasks.expansionDepth.directionSelection')} · {t(`paperTasks.expansionDepth.${record.preparation.direction_selection === 'manual' ? 'yourChoice' : 'allDirections'}`)}</p>}
            <ExpansionSteps steps={reviewExpansionDetailFlow(detail)} />
        </section>}
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
            {!record.preparation?.skipped && <PreparationPanel detail={detail} onRefresh={onRefresh} onStart={onStart} />}
            {record.result.user_instruction && record.result.user_instruction !== originalInstruction && <details className="paper-expansion-current-request">
                <summary>{t('paperTasks.currentRequest')}</summary>
                <Typography.Paragraph>{record.result.user_instruction}</Typography.Paragraph>
            </details>}
        </section>
        {completed && report?.narrative && <details className="paper-expansion-section" open>
            <summary>{t('paperTasks.researchReading')}<span>{t('paperTasks.researchReadingHint')}</span></summary>
            <section className="paper-expansion-reading" aria-label={t('paperTasks.researchReadingHint')}>
                {!!report.coverage_gaps?.length && <section aria-label={t('paperTasks.reviewFlow.incompleteResearch')}>
                    <ul>{report.coverage_gaps.map((gap, index) => <li key={index}>{[gap.direction, gap.clue_name, gap.reason].filter(Boolean).join(' · ')}</li>)}</ul>
                </section>}
                {report.relations && <PaperLineageView papers={report.input?.papers || record.result.papers}
                    relations={report.relations} structure={report.research_structure}
                    renderDetail={item => <PaperDetail item={item} analysis reading={report.reading} />} />}
                <MarkdownViewer>{report.narrative}</MarkdownViewer>
            </section>
        </details>}
    </>;
}
