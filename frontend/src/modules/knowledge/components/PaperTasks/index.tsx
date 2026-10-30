import { useCallback, useEffect, useId, useRef, useState, type ChangeEvent, type Key } from 'react';
import { createPortal } from 'react-dom';
import { Alert, Button, Checkbox, Drawer, Dropdown, Input, List, Modal, Space, Switch, Table, Tag, Tooltip, Typography, message } from 'antd';
import type { CheckboxChangeEvent } from 'antd/es/checkbox';
import { BranchesOutlined, DownOutlined, EditOutlined, FundProjectionScreenOutlined, HistoryOutlined, ReadOutlined, ReloadOutlined, CheckOutlined, MinusOutlined } from '@ant-design/icons';
import axios from 'axios';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { getChatConversationPath, CHAT_CONVERSATION_LIST_REFRESH_EVENT } from '@/modules/chat/constants/chat';
import { deleteExpansion, getExpansion, launchPaperTask, listResearchRecords, resolvePaperSelection, retryPaperContent, type Expansion, type ExpansionDetail, type PaperTask, type PaperSelection, type ReviewRecord, type ReviewRecordTarget, type WorkflowPaper } from '../../api/paperWorkflows';
import { previewPaperImport, startReferenceImport, getPaperImportStatus, type ImportPreviewItem, isImportPreviewExpired } from '../../api/academicReferences';
import './index.scss';
import ExpansionDetails from './ExpansionDetails';
import GeneratedContents from './GeneratedContents';
import ResearchRecords from './ResearchRecords';
import ReviewRecordDrawer from './ReviewRecordDrawer';
import ReviewFlow from './ReviewFlow';
import ReviewTopic from './ReviewTopic';
import ResearchDepth from './ResearchDepth';
import PaperTaskStatus from './PaperTaskStatus';
import { getExpansionCapabilities, type BudgetMode } from '../../api/paperWorkflows';
import type { PaperNotification } from '../../paper/usePaperNotifications';

interface Props {
    dataset: string;
    selected: string[];
    selectedTitles?: string[];
    canImport: boolean;
    selectionError?: string;
    onImportSubmitted: () => void;
    actionsContainer?: HTMLElement | null;
    onNotification?: (notice: PaperNotification) => void;
    onNotificationResolved?: (id: string) => void;
}
interface ResultSelection {
    source: ExpansionDetail;
    selected: string[];
    requestKey: string;
}
interface ResultConfirmation extends ResultSelection {
    kind: 'review' | 'import';
}
interface ImportPreview {
    sourceKey: string;
    items: ImportPreviewItem[];
    status: 'loading' | 'ready' | 'error';
    error?: string;
}
function resultKey(source: ExpansionDetail) {
    return `${source.record.id}:${source.record.version}`;
}
function importKey(source: ExpansionDetail, item: WorkflowPaper) {
    return JSON.stringify([source.record.dataset_id, item.paper.paper_id, item.paper.source_provider, item.paper.source_version, item.paper.document_id]);
}
function distinctPapers(papers: WorkflowPaper[]) {
    const result = new Map<string, WorkflowPaper>();
    for (const paper of papers) {
        const previous = result.get(paper.paper.paper_id);
        result.set(paper.paper.paper_id, { ...paper, is_seed: paper.is_seed || !!previous?.is_seed });
    }
    return [...result.values()];
}
const relationFields = ['seed_relation', 'direction_relation'] as const;
function currentPaperDocument(detail: ExpansionDetail, item: WorkflowPaper, preview?: ImportPreviewItem) {
    const documents = [...(detail.presence?.find(entry => entry.work_id === item.paper.paper_id)?.current_dataset || []), ...(preview?.presence.current_dataset || [])];
    const content = detail.contents.find(content => content.paper_id === item.paper.paper_id);
    return documents.find(document => document.dataset_id === detail.record.dataset_id && document.document_id && (
        item.paper.document_id ? document.document_id === item.paper.document_id : item.paper.source_version
            ? document.source_version === item.paper.source_version && document.source_provider === item.paper.source_provider
            : item.is_seed || !!content?.source_hash && document.content_sha256 === content.source_hash
    ));
}

function reviewInstruction(topic: string, additional: string) {
    return [topic.trim() && `综述主题：${topic.trim()}`, additional.trim() && `补充要求：${additional.trim()}`].filter(Boolean).join('\n\n');
}

export default function PaperTasks({ dataset, selected, selectedTitles = [], selectionError, canImport, onImportSubmitted, actionsContainer, onNotification, onNotificationResolved }: Props) {
    const { t } = useTranslation();
    const paperDetailsId = useId();
    const navigate = useNavigate();
    const [params, setParams] = useSearchParams();
    const [launch, setLaunch] = useState<{ kind: PaperTask; direction: boolean }>();
    const [instruction, setInstruction] = useState('');
    const [reflectionFirst, setReflectionFirst] = useState(false);
    const [expansionEnabled, setExpansionEnabled] = useState(false);
    const [topicBusy, setTopicBusy] = useState(false);
    const [selectionRetry, setSelectionRetry] = useState(0);
    const [reviewPapersExpanded, setReviewPapersExpanded] = useState(false);
    const [reviewTopic, setReviewTopic] = useState('');
    const [reviewSelection, setReviewSelection] = useState<PaperSelection>();
    const [selectionLoading, setSelectionLoading] = useState(false);
    const [selectionFailure, setSelectionFailure] = useState('');
    const directReview = launch?.kind === 'review' && !launch.direction && !expansionEnabled;
    const eligibleReviewPapers = reviewSelection?.papers.filter(paper => reviewSelection.materials?.some(material => material.paper_id === paper.paper.paper_id && material.eligible)) || [];
    useEffect(() => {
        if (!directReview) { setReviewSelection(undefined); setSelectionLoading(false); return; }
        let active = true;
        let timer: ReturnType<typeof setTimeout>;
        setReviewSelection(undefined);
        setSelectionFailure('');
        setSelectionLoading(true);
        const refresh = async () => {
            try {
                const result = await resolvePaperSelection(dataset, selected, true, true);
                if (!active) return;
                setReviewSelection(result);
                setSelectionFailure('');
                if (result.materials?.some(item => ['preparing', 'processing', 'pending'].includes(item.status))) timer = setTimeout(() => void refresh(), 4000);
            } catch (error) {
                if (active) setSelectionFailure(axios.isAxiosError(error) && typeof error.response?.data?.message === 'string' ? error.response.data.message : t('paperTasks.reviewSelectionFailed'));
            } finally { if (active) setSelectionLoading(false); }
        };
        void refresh();
        return () => { active = false; clearTimeout(timer); };
    }, [directReview, dataset, JSON.stringify(selected), selectionRetry, t]);
    const [budgetMode, setBudgetMode] = useState<BudgetMode>('balanced');
    const [capabilities, setCapabilities] = useState<{openalex_key: boolean; sciverse: boolean; web_search: boolean}>();
    useEffect(() => { if (launch) void getExpansionCapabilities().then(setCapabilities).catch(() => setCapabilities(undefined)); }, [launch]);
    const [busy, setBusy] = useState(false);
    const [retryingContents, setRetryingContents] = useState(false);
    const [history, setHistory] = useState(false);
    const [historyError, setHistoryError] = useState(false);
    const [historyLoading, setHistoryLoading] = useState(false);
    const [records, setRecords] = useState<Expansion[]>([]);
    const [reviewRecords, setReviewRecords] = useState<ReviewRecord[]>([]);
    const [reviewRecord, setReviewRecord] = useState<ReviewRecordTarget>();
    const [detail, setDetail] = useState<ExpansionDetail>();
    const [expandedPaperIds, setExpandedPaperIds] = useState<string[]>([]);
    const [importBatches, setImportBatches] = useState<{ id: string; keys: string[] }[]>([]);
    const importsChanged = useRef(onImportSubmitted);
    importsChanged.current = onImportSubmitted;
    const [selection, setSelection] = useState<ResultSelection>();
    const [confirmation, setConfirmation] = useState<ResultConfirmation>();
    const [preview, setPreview] = useState<ImportPreview>();
    const [actionError, setActionError] = useState('');
    const submitting = useRef<'review' | 'import'>();
    const importRequestKeys = useRef(new Map<string, string>());
    const requestKey = useRef('');
    const requestInput = useRef('');
    const previewRequest = useRef(0);
    const pendingPreview = useRef<{ key: string; promise: Promise<ImportPreviewItem[]> }>();
    const pendingDetail = useRef<{ id: string; promise: Promise<ExpansionDetail> }>();
    const detailRequest = useRef(0);
    const detailTarget = useRef<string>();
    const taskLabel = (kind: string) => t(`paperTasks.task.${kind}`, { defaultValue: kind });
    const statusLabel = (status: string) => (({ preparing: '初步调研中', awaiting_info: '待补充信息', awaiting_confirmation: '待确认方向', preparation_failed: '初步调研失败' } as Record<string, string>)[status] || t(`paperTasks.status.${status}`, { defaultValue: status }));
    const selectedPaperTaskError = selectionError || (!selected.length ? t('paperTasks.selectPaperFirst') : '');
    const openLaunch = useCallback((kind: PaperTask, direction: boolean) => {
        if (direction && selected.length) return;
        if (!direction) {
            if (selectedPaperTaskError) { message.warning(selectedPaperTaskError); return; }
        }
        setActionError('');
        setInstruction('');
        setReflectionFirst(false);
        setExpansionEnabled(false);
        setReviewTopic('');
        setReviewPapersExpanded(false);
        setBudgetMode('balanced');
        setLaunch({ kind, direction });
        requestKey.current = crypto.randomUUID();
        requestInput.current = '';
    }, [selectedPaperTaskError, selected.length, selectedTitles]);
    const refreshHistory = async () => {
        setHistoryLoading(true);
        try {
            const result = await listResearchRecords(dataset, !!onNotification);
            setRecords(result.items || []);
            setReviewRecords(result.reviews || []);
            setHistoryError(false);
            onNotificationResolved?.('expansion-history');
        } catch (error) {
            setHistoryError(true);
            onNotification?.({ id: 'expansion-history', title: t('paperTasks.recordsLoadFailed'), message: error instanceof Error ? error.message : '记录加载失败，请重试', action: { label: '重新加载', onClick: refreshHistory } });
        } finally {
            setHistoryLoading(false);
        }
    };
    const openHistory = () => {
        setHistory(true);
        void refreshHistory().catch(() => {});
    };
    const openReviewRecord = (record: ReviewRecordTarget) => {
        closeDetail();
        setReviewRecord(record);
        if (!history) openHistory();
    };
    const openDetail = async (id: string) => {
        if (detailTarget.current !== id) {
            setExpandedPaperIds([]);
            setSelection(undefined);
            setConfirmation(undefined);
            setPreview(undefined);
            setActionError('');
            importRequestKeys.current.clear();
            previewRequest.current += 1;
            pendingDetail.current = undefined; pendingPreview.current = undefined;
        }
        detailTarget.current = id;
        const request = ++detailRequest.current;
        const pending = pendingDetail.current?.id === id ? pendingDetail.current : { id, promise: getExpansion(id, !!onNotification) };
        pendingDetail.current = pending;
        try {
            const result = await pending.promise;
            if (request === detailRequest.current) {
                if (result.record.status !== 'completed') {
                    setSelection(undefined);
                    setConfirmation(undefined);
                    setPreview(undefined);
                    previewRequest.current += 1;
                }
                setDetail(result);
                setParams(current => { if (current.get('expansion_id') === id) return current; const next = new URLSearchParams(current); next.set('expansion_id', id); return next; }, { replace: true });
                onNotificationResolved?.(`expansion-detail:${id}`);
                for (const content of result.record.status === 'completed' ? result.contents : []) {
                    const noticeId = `expansion-content:${id}:${content.paper_id}`;
                    if (content.status === 'failed' && content.error) {
                        const paper = result.record.result.papers.find(item => item.paper.paper_id === content.paper_id)?.paper;
                        onNotification?.({ id: noticeId, title: `《${paper?.title || content.paper_id}》全文获取失败`, message: content.error, action: { label: '查看扩展结果', closeOnClick: true, onClick: () => openDetail(id).catch(() => {}) } });
                    } else onNotificationResolved?.(noticeId);
                }
            }
        } catch (error) {
            if (request === detailRequest.current) onNotification?.({ id: `expansion-detail:${id}`, title: '论文扩展结果加载失败', message: error instanceof Error ? error.message : '扩展结果加载失败', action: { label: '重新加载', closeOnClick: true, onClick: () => openDetail(id).catch(() => {}) } });
            throw error;
        } finally { if (pendingDetail.current === pending) pendingDetail.current = undefined; }
    };
    useEffect(() => {
        const open = (event: Event) => { const id = (event as CustomEvent<{id: string}>).detail?.id; if (id) void openDetail(id).catch(() => {}); };
        window.addEventListener('paper-expansion-open', open);
        return () => window.removeEventListener('paper-expansion-open', open);
    }, [dataset]);
    useEffect(() => {
        if (detail) document.documentElement.dataset.paperExpansion = detail.record.id;
        return () => { delete document.documentElement.dataset.paperExpansion; };
    }, [detail?.record.id]);
    const expansionID = params.get('expansion_id');
    useEffect(() => {
        if (expansionID && detailTarget.current !== expansionID) void openDetail(expansionID).catch(() => {});
    }, [expansionID]);
    const resultReady = detail?.record.status === 'completed';
    const detailActive = !!detail && (['pending', 'running', 'active', 'preparing'].includes(detail.record.status)
        || detail.contents.some(content => ['pending', 'processing'].includes(content.status))
        || detail.presence?.some(item => item.importing));
    useEffect(() => {
        if (!detail) return;
        let running = false;
        const refresh = async () => {
            if (running || detailTarget.current !== detail.record.id) return;
            running = true;
            try { await openDetail(detail.record.id); } catch { /* Keep the last saved result visible. */ }
            finally { running = false; }
        };
        const onFocus = () => { void refresh(); };
        window.addEventListener('focus', onFocus);
        const timer = detailActive ? window.setInterval(onFocus, 12000) : undefined;
        return () => { window.removeEventListener('focus', onFocus); if (timer) window.clearInterval(timer); };
    }, [detail?.record.id, detailActive]);
    useEffect(() => () => { previewRequest.current += 1; detailRequest.current += 1; detailTarget.current = undefined; }, []);
    useEffect(() => {
        if (!importBatches.length) return;
        let active = true;
        let running = false;
        const refresh = async () => {
            if (running) return;
            running = true;
            try {
                const statuses = await Promise.all(importBatches.map(async batch => ({ id: batch.id, status: await getPaperImportStatus(batch.id) })));
                if (!active) return;
                const completed = statuses.filter(item => ['succeeded', 'completed_with_errors', 'failed', 'canceled'].includes(item.status)).map(item => item.id);
                if (completed.length) {
                    if (!onNotification && statuses.some(item => ['failed', 'completed_with_errors'].includes(item.status))) setActionError(t('paperTasks.importIncomplete'));
                    setImportBatches(current => current.filter(batch => !completed.includes(batch.id)));
                    importsChanged.current();
                    if (detailTarget.current) void openDetail(detailTarget.current).then(() => { if (detail) void refreshImportPreview(detail); }).catch(() => {});
                }
            } catch {
                // The persisted import continues; retry its status on the next tick.
            } finally { running = false; }
        };
        void refresh();
        const timer = window.setInterval(() => void refresh(), 12000);
        return () => { active = false; window.clearInterval(timer); };
    }, [dataset, importBatches]);
    const goChat = (id: string, autoReview = false) => {
        window.dispatchEvent(new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT));
        navigate(getChatConversationPath(id) + (autoReview ? '?paper_auto_review=1' : ''));
    };
    const submit = async () => {
        if (!launch || busy || topicBusy) return;
        if (!launch.direction && selectedPaperTaskError) {
            message.warning(selectedPaperTaskError);
            return;
        }
        if (launch.direction && !instruction.trim()) {
            message.warning(t('paperTasks.enterTopic'));
            return;
        }
        setBusy(true);
        try {
            if (directReview && (selectionLoading || selectionFailure || eligibleReviewPapers.length < 2)) return;
            if (!launch.direction && !directReview) {
                const selection = await resolvePaperSelection(dataset, selected, !!onNotification);
                if (selection.invalid.length) {
                    Modal.error({ title: t('paperTasks.invalidSelection'), content: <List dataSource={selection.invalid} renderItem={item => <List.Item>{item.document_id}：{item.reason}</List.Item>} /> });
                    return;
                }
                if (!selection.papers.length) {
                    message.warning(t('paperTasks.selectPaperFirst'));
                    return;
                }
            }
            const body = {
                kind: launch.kind, dataset_id: dataset, document_ids: launch.direction ? [] : selected,
                direction: launch.direction ? instruction.trim() : undefined,
                user_instruction: launch.direction ? undefined : (launch.kind === 'review' ? reviewInstruction(reviewTopic, instruction) : instruction.trim()), reflection_first: reflectionFirst, budget_mode: budgetMode,
                ...(directReview ? { skip_expansion: true, review_scope: 'selected' as const, paper_ids: eligibleReviewPapers.map(item => item.paper.paper_id) } : {}),
            };
            const fingerprint = JSON.stringify(body);
            if (requestInput.current && requestInput.current !== fingerprint) requestKey.current = crypto.randomUUID();
            requestInput.current = fingerprint;
            const result = await launchPaperTask({ ...body, request_key: requestKey.current }, !!onNotification);
            onNotificationResolved?.('paper-task-launch');
            setLaunch(undefined);
            if (result.expansion_id && ['expansion', 'review'].includes(launch.kind)) await openDetail(result.expansion_id);
            else if (result.conversation_id) goChat(result.conversation_id, launch.kind === 'review');
            else throw new Error('任务未返回有效记录，请重试');
        } catch (error) {
            if (onNotification) {
                setActionError('任务尚未创建，请重试或查看通知。');
                onNotification({ id: 'paper-task-launch', title: '论文任务创建失败', message: error instanceof Error ? error.message : '任务创建失败，请重试' });
            }
        } finally {
            setBusy(false);
        }
    };
    const contentReady = (source: ExpansionDetail, id: string) => source.contents.some(content => content.paper_id === id && content.status === 'ready' && !!content.content_hash && !!content.quality?.eligible);
    const contentStatus = (source: ExpansionDetail, id: string) => {
        const content = source.contents.find(item => item.paper_id === id);
        if (!content) return t('paperTasks.content.not_started');
        if (content.status === 'needs_review') return t(`paperTasks.content.${content.quality?.downloaded ? 'downloaded_needs_review' : 'needs_review'}`);
        if (content.status === 'processing') return t(`paperTasks.content.${content.quality?.downloaded ? 'parsing' : content.quality?.acquisition?.started_at ? 'processing' : 'pending'}`);
        if (content.status === 'failed' && content.quality?.downloaded) return t('paperTasks.content.downloaded_failed');
        return t(`paperTasks.content.${content.status === 'ready' && !content.content_hash ? 'not_started' : content.status}`);
    };
    // Keep the selected result snapshot stable while polling live content and import status.
    const source = detail && selection?.source.record.id === detail.record.id
        ? (detail.record.version === selection.source.record.version ? { ...detail, record: selection.source.record } : selection.source) : detail;
    const papers = source ? distinctPapers(source.record.result.papers || []) : [];
    const retryableContents = source ? papers.filter(item => !contentReady(source, item.paper.paper_id)
        && !source.contents.some(content => content.paper_id === item.paper.paper_id && ['pending', 'processing'].includes(content.status))) : [];
    const retryContents = async (target: ExpansionDetail, ids: string[]) => {
        setRetryingContents(true);
        try {
            const results = await Promise.allSettled(ids.map(id => retryPaperContent(id, target.record.id, true)));
            const failedTitles: string[] = [];
            results.forEach((result, index) => {
                const id = ids[index];
                if (result.status === 'fulfilled') onNotificationResolved?.(`content-retry:${id}`);
                else {
                    const title = target.record.result.papers.find(item => item.paper.paper_id === id)?.paper.title || id;
                    failedTitles.push(title);
                    onNotification?.({ id: `content-retry:${id}`, title: `《${title}》全文重试失败`, message: result.reason instanceof Error ? result.reason.message : t('paperTasks.retryContentFailed') });
                }
            });
            setActionError(!onNotification && failedTitles.length ? t('paperTasks.retryContentsFailed', { titles: failedTitles.join('、') }) : '');
            await openDetail(target.record.id);
        } catch (error) {
            if (!onNotification) setActionError(error instanceof Error ? error.message : t('paperTasks.retryContentFailed'));
        } finally { setRetryingContents(false); }
    };
    const refreshImportPreview = async (target: ExpansionDetail) => {
        if (!canImport || !resultReady || target.record.status !== 'completed') return;
        const sourceKey = resultKey(target);
        const ids = distinctPapers(target.record.result.papers || []).map(item => item.paper.paper_id);
        const request = ++previewRequest.current;
        setPreview(current => current?.sourceKey === sourceKey && current.status === 'ready'
            ? current : { sourceKey, items: [], status: 'loading' });
        try {
            const key = `${sourceKey}:${ids.join(',')}`;
            const pending = pendingPreview.current?.key === key ? pendingPreview.current : { key, promise: ids.length ? previewPaperImport(ids, target.record.dataset_id, target.record.id, target.record.version, !!onNotification) : Promise.resolve([]) };
            pendingPreview.current = pending;
            let items: ImportPreviewItem[];
            try { items = await pending.promise; }
            finally { if (pendingPreview.current === pending) pendingPreview.current = undefined; }
            if (request === previewRequest.current) { setPreview({ sourceKey, items, status: 'ready' }); onNotificationResolved?.(`expansion-preview:${target.record.id}`); }
        } catch (error) {
            if (request === previewRequest.current) {
                const reason = axios.isAxiosError(error) && error.response?.data?.code === 'EXPANSION_VERSION_UNAVAILABLE'
                    ? t('paperTasks.importVersionUnavailable') : t('paperTasks.previewFailed');
                setPreview({ sourceKey, items: [], status: 'error', error: reason });
                onNotification?.({ id: `expansion-preview:${target.record.id}`, title: '论文导入预览加载失败', message: reason, action: { label: '重新加载预览', onClick: () => refreshImportPreview(target) } });
            }
        }
    };
    useEffect(() => {
        if (source && resultReady) void refreshImportPreview(source);
    }, [source ? resultKey(source) : '', JSON.stringify(papers.map(item => [item.paper.paper_id, item.paper.source_version])), JSON.stringify(detail?.presence), canImport, resultReady]);
    useEffect(() => {
        const onFocus = () => { if (source && resultReady) void refreshImportPreview(source); };
        window.addEventListener('focus', onFocus);
        return () => window.removeEventListener('focus', onFocus);
    }, [source ? resultKey(source) : '', canImport, resultReady]);
    const previewMatches = (item: WorkflowPaper, entry?: ImportPreviewItem) => !!entry && (!item.paper.source_version ||
        (entry.paper?.source_version === item.paper.source_version && entry.paper.source_provider === item.paper.source_provider));
    const previewItem = (target: ExpansionDetail, item: WorkflowPaper) => {
        const entry = preview?.sourceKey === resultKey(target) ? preview.items.find(value => value.work_id === item.paper.paper_id) : undefined;
        return previewMatches(item, entry) ? entry : undefined;
    };
    const importing = (target: ExpansionDetail, item: WorkflowPaper) => importBatches.some(batch => batch.keys.includes(importKey(target, item)))
        || previewItem(target, item)?.disposition === 'importing'
        || (detail?.record.version === target.record.version && target.presence?.some(entry => entry.work_id === item.paper.paper_id && entry.importing));
    const importReason = (target: ExpansionDetail, item: WorkflowPaper) => {
        const entry = previewItem(target, item);
        if (currentPaperDocument(target, item, entry) || entry?.disposition === 'already_present') return t('paperTasks.inCurrentCollection');
        if (!canImport) return t('paperTasks.importForbidden');
        if (importing(target, item)) return t('paperTasks.importStatus.importing');
        if (preview?.sourceKey !== resultKey(target) || preview.status === 'loading') return t('paperTasks.checkingImport');
        if (preview.status === 'error') return preview.error || t('paperTasks.previewFailed');
        const candidate = preview.items.find(value => value.work_id === item.paper.paper_id);
        if (!candidate) return t('paperTasks.previewFailed');
        if (!previewMatches(item, candidate)) return t('paperTasks.importSourceMismatch');
        return entry?.disposition === 'downloadable' && entry.fulltext_candidates.length ? '' : t('paperTasks.importStatus.metadata_only');
    };
    const changeSelection = (keys: Key[]) => {
        if (!resultReady || !source || busy || confirmation) return;
        setSelection(current => keys.length ? { source: current?.source || source, selected: keys.map(String), requestKey: crypto.randomUUID() } : undefined);
        setActionError('');
    };
    const selectedPapers = papers.filter(item => selection?.selected.includes(item.paper.paper_id));
    const importable = source ? selectedPapers.filter(item => !importReason(source, item)) : [];
    const actionReason = busy ? t('paperTasks.submitting') : confirmation ? t('paperTasks.finishImportConfirmation') : '';
    const reviewReason = actionReason || (!source?.record.version ? t('paperTasks.resultPending') : !papers.length ? t('paperTasks.noPapersYet') : '');
    const bulkImportReason = actionReason || (!canImport ? t('paperTasks.importForbidden')
        : !papers.length ? t('paperTasks.noPapersYet') : '');
    const openConfirmation = (kind: ResultConfirmation['kind']) => {
        if (!resultReady || !source || busy || confirmation) return;
        setActionError('');
        if (kind === 'review') { setReviewTopic(''); setInstruction(''); }
        setConfirmation({ source, selected: selection?.selected || [], requestKey: selection?.requestKey || crypto.randomUUID(), kind });
    };
    const submitReview = async () => {
        if (!resultReady || !confirmation || confirmation.kind !== 'review' || confirmationEligible.length < 2 || submitting.current || busy || topicBusy) return;
        const target = confirmation.source;
        submitting.current = 'review';
        setBusy(true);
        setActionError('');
        try {
            const result = await launchPaperTask({
                kind: 'review', dataset_id: target.record.dataset_id,
                expansion_id: target.record.id, expansion_version: target.record.version,
                user_instruction: reviewInstruction(reviewTopic, instruction),
                paper_ids: confirmationEligible.map(item => item.paper.paper_id), request_key: confirmation.requestKey,
            }, !!onNotification);
            onNotificationResolved?.(`expansion-review:${target.record.id}`);
            if (!result.conversation_id) throw new Error('任务未返回 Chat，请重试');
            setConfirmation(undefined);
            setSelection(current => current ? { ...current, requestKey: crypto.randomUUID() } : current);
            goChat(result.conversation_id);
        } catch (error) {
            setActionError(onNotification ? '提交尚未完成，请重试或查看通知。' : t('paperTasks.submitFailed'));
            onNotification?.({ id: `expansion-review:${target.record.id}`, title: '论文综述任务创建失败', message: error instanceof Error ? error.message : '任务创建失败' });
        } finally {
            submitting.current = undefined;
            setBusy(false);
        }
    };
    const submitImport = async (target: ExpansionDetail, chosen: WorkflowPaper[], key: string) => {
        if (!resultReady || !canImport || !chosen.length || chosen.some(item => importReason(target, item)) || submitting.current || busy || topicBusy) return;
        submitting.current = 'import';
        setBusy(true);
        setActionError('');
        try {
            const items = chosen.map(item => previewItem(target, item)!);
            const result = await startReferenceImport(target.record.dataset_id, [], items, key, !!onNotification);
            onNotificationResolved?.(`expansion-import:${target.record.id}`);
            setImportBatches(current => [...current, { id: result.batch_id, keys: chosen.map(item => importKey(target, item)) }]);
            setPreview(current => current?.sourceKey === resultKey(target) ? {
                ...current,
                items: current.items.map(item => chosen.some(paper => paper.paper.paper_id === item.work_id)
                    ? { ...item, disposition: 'importing', presence: { ...item.presence, importing: true } } : item),
            } : current);
            chosen.forEach(item => importRequestKeys.current.delete(importKey(target, item)));
            setSelection(current => current ? { ...current, requestKey: crypto.randomUUID() } : current);
            setConfirmation(undefined);
            importsChanged.current();
            message.success(t('paperTasks.importSubmitted'));
            if (detailTarget.current === target.record.id) void openDetail(target.record.id).catch(() => {});
        } catch (error) {
            if (isImportPreviewExpired(error)) {
                await refreshImportPreview(target);
                setActionError('导入预览已刷新，请确认论文及版本后重试');
                return;
            }
            setActionError(onNotification ? '导入尚未提交，请重试或查看通知。' : t('paperTasks.submitFailed'));
            onNotification?.({ id: `expansion-import:${target.record.id}`, title: '扩展论文导入提交失败', message: error instanceof Error ? error.message : '导入提交失败' });
        } finally {
            submitting.current = undefined;
            setBusy(false);
        }
    };
    const importPaper = (target: ExpansionDetail, item: WorkflowPaper) => {
        if (!resultReady) return;
        const fingerprint = importKey(target, item);
        let key = importRequestKeys.current.get(fingerprint);
        if (!key) { key = crypto.randomUUID(); importRequestKeys.current.set(fingerprint, key); }
        setActionError('');
        setConfirmation({ source: target, selected: [item.paper.paper_id], requestKey: key, kind: 'import' });
    };
    const cancelSelection = () => {
        if (submitting.current) return;
        setSelection(undefined);
        setConfirmation(undefined);
        setActionError('');
    };
    const closeDetail = () => {
        if (submitting.current) return;
        previewRequest.current += 1;
        detailRequest.current += 1;
        detailTarget.current = undefined;
        pendingDetail.current = undefined; pendingPreview.current = undefined;
        setDetail(undefined);
        setPreview(undefined);
        importRequestKeys.current.clear();
        cancelSelection();
        if (params.has('expansion_id')) {
            const next = new URLSearchParams(params);
            next.delete('expansion_id');
            setParams(next);
        }
    };
    const available = source ? papers.filter(item => contentReady(source, item.paper.paper_id)).length : 0;
    const confirmationPapers = confirmation ? distinctPapers(confirmation.source.record.result.papers || []).filter(item => confirmation.selected.includes(item.paper.paper_id)) : [];
    const confirmationEligible = confirmation ? confirmationPapers.filter(item => confirmation.kind === 'review'
        ? contentReady(confirmation.source, item.paper.paper_id) : !importReason(confirmation.source, item)) : [];
    const confirmationReason = confirmation?.kind === 'review'
        ? confirmationEligible.length < 2 ? t('paperTasks.reviewMinimumFulltext') : ''
        : !canImport ? t('paperTasks.importForbidden') : !confirmationEligible.length ? t('paperTasks.noImportableSelection') : '';
    const activeTask = launch?.kind;
    const reviewDetail = detail?.record.goal === 'review';
    const actions = <div className="paper-library-task-actions" role="group" aria-label="论文工具">
            <Dropdown trigger={['click']} placement="topLeft" menu={{ items: [
                { key: 'direction', label: t('paperTasks.expandDirection'), disabled: selected.length > 0, title: selected.length ? t('paperTasks.clearForDirection') : undefined, onClick: () => openLaunch('expansion', true) },
                { type: 'divider' },
                { key: 'papers', className: 'paper-library-task-selection-action', label: <Tooltip title={selectedPaperTaskError || undefined} placement="right"><span className="paper-library-task-selection-label">{t('paperTasks.expandPapers')}</span></Tooltip>, disabled: !!selectedPaperTaskError, onClick: () => openLaunch('expansion', false) },
            ] }}><Button type="text" className={`paper-library-task-action ${activeTask === 'expansion' ? 'is-active' : ''}`} icon={<BranchesOutlined aria-hidden />} aria-pressed={activeTask === 'expansion'}>{taskLabel('expansion')} <DownOutlined aria-hidden /></Button></Dropdown>
            <Dropdown trigger={['click']} placement="topLeft" menu={{ items: [
                { key: 'history', label: t('paperTasks.reviewHistory'), onClick: openHistory },
                { key: 'direction', label: t('paperTasks.reviewDirection'), disabled: selected.length > 0, title: selected.length ? t('paperTasks.clearForDirection') : undefined, onClick: () => openLaunch('review', true) },
                { type: 'divider' },
                { key: 'papers', className: 'paper-library-task-selection-action', label: <Tooltip title={selectedPaperTaskError || undefined} placement="right"><span className="paper-library-task-selection-label">{t('paperTasks.reviewPapers')}</span></Tooltip>, disabled: !!selectedPaperTaskError, onClick: () => openLaunch('review', false) },
            ] }}><Button type="text" className={`paper-library-task-action ${activeTask === 'review' ? 'is-active' : ''}`} icon={<ReadOutlined aria-hidden />} aria-pressed={activeTask === 'review'}>{taskLabel('review')} <DownOutlined aria-hidden /></Button></Dropdown>
            <Tooltip title={selectedPaperTaskError || undefined} placement="right"><span><Button type="text" className={`paper-library-task-action ${activeTask === 'reflection' ? 'is-active' : ''}`} icon={<EditOutlined aria-hidden />} aria-pressed={activeTask === 'reflection'} disabled={!!selectedPaperTaskError} onClick={() => openLaunch('reflection', false)}>{taskLabel('reflection')}</Button></span></Tooltip>
            <Tooltip title={selectedPaperTaskError || undefined} placement="right"><span><Button type="text" className={`paper-library-task-action ${activeTask === 'ppt' ? 'is-active' : ''}`} icon={<FundProjectionScreenOutlined aria-hidden />} aria-pressed={activeTask === 'ppt'} disabled={!!selectedPaperTaskError} onClick={() => openLaunch('ppt', false)}>{taskLabel('ppt')}</Button></span></Tooltip>
            <Button type="text" className="paper-library-task-action" icon={<HistoryOutlined aria-hidden />} onClick={openHistory}>{t('paperTasks.records')}</Button>
        </div>;
    const confirmDeleteExpansion = () => {
        if (!detail) return;
        Modal.confirm({
            title: t('paperTasks.deleteTitle'), content: t('paperTasks.deleteHint'), okText: t('paperTasks.confirmDelete'), cancelText: t('paperTasks.cancel'), okButtonProps: { danger: true },
            onOk: async () => {
                try { await deleteExpansion(detail.record.id, !!onNotification); onNotificationResolved?.(`expansion-delete:${detail.record.id}`); closeDetail(); await refreshHistory(); }
                catch (error) { onNotification?.({ id: `expansion-delete:${detail.record.id}`, title: '论文扩展记录删除失败', message: error instanceof Error ? error.message : '删除失败，请重试' }); throw error; }
            },
        });
    };
    const reviewPaperRows = reviewSelection ? [
        ...reviewSelection.papers.map(item => {
            const material = reviewSelection.materials?.find(value => value.paper_id === item.paper.paper_id);
            return { id: item.paper.paper_id, title: item.paper.title || item.paper.paper_id,
                description: material?.eligible ? t('paperTasks.reviewIncluded') : material?.reason || t('paperTasks.reviewPreparing') };
        }),
        ...reviewSelection.invalid.map(item => ({ id: item.document_id, title: item.document_id, description: item.reason })),
    ] : selected.map((id, index) => ({ id, title: selectedTitles[index] || id,
        description: t(selectionFailure ? 'paperTasks.materialPending' : 'paperTasks.reviewPreparing') }));
    return <>
        {actionsContainer ? createPortal(actions, actionsContainer) : actions}
        <Modal okButtonProps={{ disabled: topicBusy || (directReview && (selectionLoading || !!selectionFailure || eligibleReviewPapers.length < 2)) }} okText={directReview ? t('paperTasks.confirmAndContinue') : launch && ['expansion', 'review'].includes(launch.kind) ? '开始初步调研' : t('paperTasks.start')} cancelText={t('paperTasks.cancel')} open={!!launch}
            title={launch ? taskLabel(launch.kind) : ''} className={launch && ['expansion', 'review'].includes(launch.kind) ? 'paper-task-launch-modal' : undefined} width={launch && ['expansion', 'review'].includes(launch.kind) ? 660 : 520}
            closable={!busy && !topicBusy} maskClosable={!busy && !topicBusy} keyboard={!busy && !topicBusy} onCancel={() => !busy && !topicBusy && setLaunch(undefined)} onOk={() => void submit()} confirmLoading={busy}>
            {actionError && onNotification && <p role="status">{actionError}</p>}
            {launch?.kind === 'review' && <ReviewFlow entry direct={directReview} />}
            {directReview && <>
                <List id="review-selected-papers" loading={selectionLoading} rowKey="id"
                    dataSource={reviewPapersExpanded ? reviewPaperRows : reviewPaperRows.slice(0, 3)}
                    renderItem={item => <List.Item><List.Item.Meta title={item.title} description={item.description} /></List.Item>} />
                {reviewPaperRows.length > 3 && <Button type="link" aria-expanded={reviewPapersExpanded} aria-controls="review-selected-papers"
                    onClick={() => setReviewPapersExpanded(value => !value)}>
                    {t(reviewPapersExpanded ? 'paperTasks.collapseSelectedPapers' : 'paperTasks.expandSelectedPapers', { count: reviewPaperRows.length })}
                </Button>}
                {!reviewSelection && <Typography.Paragraph type="secondary">{t('paperTasks.selectionPending', { count: selected.length })}</Typography.Paragraph>}
                {selectionFailure && <Alert type="error" showIcon message={selectionFailure} action={<Button onClick={() => setSelectionRetry(value => value + 1)}>{t('paperTasks.retry')}</Button>} />}
                {!selectionLoading && !selectionFailure && eligibleReviewPapers.length < 2 && <Alert type="warning" message={t('paperTasks.reviewMinimumFulltext')} />}
                {reviewSelection && <Typography.Paragraph type="secondary">{t('paperTasks.reviewSummary', { count: eligibleReviewPapers.length, selected: (reviewSelection?.papers.length || 0) + (reviewSelection?.invalid.length || 0), skipped: (reviewSelection?.papers.length || 0) + (reviewSelection?.invalid.length || 0) - eligibleReviewPapers.length })}</Typography.Paragraph>}
            </>}
            {launch && !launch.direction && !directReview && <div className="paper-task-launch-selection">
                <Typography.Text type="secondary">{t('paperTasks.selectedLibraryPapers', { count: selected.length })}</Typography.Text>
                {selectedTitles.length > 0 && <Typography.Paragraph type="secondary" ellipsis={{ rows: 2, tooltip: true }}>{selectedTitles.join(' · ')}</Typography.Paragraph>}
            </div>}
            {launch?.kind === 'review' && !launch.direction && <ReviewTopic value={reviewTopic} onChange={setReviewTopic}
                input={{ dataset_id: dataset, document_ids: selected, user_instruction: instruction }} disabled={busy}
                onBusyChange={setTopicBusy} />}
            {launch?.kind === 'review' && !launch.direction && <div className="paper-review-skip-row">
                <div><Typography.Text>{t('paperTasks.enableExpansion')}</Typography.Text><div><Typography.Text type="secondary">{t(expansionEnabled ? 'paperTasks.enableExpansionHint' : 'paperTasks.noExpansionHint')}</Typography.Text></div></div>
                <Switch checked={expansionEnabled} onChange={setExpansionEnabled} disabled={busy || topicBusy} aria-label={t('paperTasks.enableExpansion')} />
            </div>}
            {launch && !directReview && ['expansion', 'review'].includes(launch.kind) && <Space direction="vertical" style={{ width: '100%', marginTop: 12 }}>
                <ResearchDepth value={budgetMode} onChange={setBudgetMode} hint={launch.kind === 'review' ? t('paperTasks.reviewFlow.depthHint') : undefined} />
                <p className="paper-task-launch-flow-hint"><BranchesOutlined />{launch.kind === 'review' ? t('paperTasks.reviewFlow.directionsHint') : '有多个方向时请你选择；只有一个方向时自动继续拓展。'}</p>
                {capabilities && (!capabilities.openalex_key || !capabilities.sciverse) && <Alert type="info" message="可使用当前配置开始调研" description={<>
                  {!capabilities.openalex_key && <div>OpenAlex 使用基础访问，部分检索深度与托管全文受限。<Link to="/settings?section=knowledge&tool=academic-search&editor=service&item=openalex">配置 OpenAlex</Link></div>}
                  {!capabilities.sciverse && <div>Sciverse 检索未启用。<Link to="/settings?section=knowledge&tool=academic-search&editor=service&item=sciverse">配置 Sciverse</Link></div>}
                </>} />}
            </Space>}
            <Typography.Paragraph style={{ marginTop: 12, marginBottom: 4 }}>{t(launch?.direction ? 'paperTasks.topic' : 'paperTasks.additional')}</Typography.Paragraph>
            <Input.TextArea aria-label={t(launch?.direction ? 'paperTasks.topic' : 'paperTasks.additional')}
                placeholder={t(launch?.direction ? 'paperTasks.topicPlaceholder' : `paperTasks.placeholder.${launch?.kind || 'expansion'}`)}
                value={instruction} disabled={busy || topicBusy} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setInstruction(event.target.value)} />
            {launch?.kind === 'ppt' && <Checkbox style={{ marginTop: 12 }} checked={reflectionFirst} onChange={(event: CheckboxChangeEvent) => setReflectionFirst(event.target.checked)}>{t('paperTasks.reflectionFirst')}</Checkbox>}
        </Modal>
        {/* Keep nested portals mounted for result links that open without history. */}
        <Drawer forceRender className="paper-expansion-history-drawer" open={history} width={780} onClose={() => setHistory(false)}
            title={<div className="paper-expansion-history-heading"><span>{t('paperTasks.personalRecords')}</span><span className="paper-expansion-history-count">{t('paperTasks.recordCount', { count: records.length + reviewRecords.length })}</span></div>}
            extra={<Button type="text" className="paper-expansion-history-refresh" icon={<ReloadOutlined />} loading={historyLoading} onClick={() => void refreshHistory()}>{t('paperTasks.refresh')}</Button>}>
            <ResearchRecords expansions={records} reviews={reviewRecords} open={history} loading={historyLoading} error={historyError}
                statusLabel={statusLabel} onExpansion={id => void openDetail(id).catch(() => {})} onReview={openReviewRecord} />
            <ReviewRecordDrawer record={reviewRecord} onClose={() => setReviewRecord(undefined)} onChat={goChat}
                onSource={id => { void openDetail(id).then(() => setReviewRecord(undefined)).catch(() => {}); }} />
            <Drawer className="paper-expansion-detail-drawer" open={!!detail} title={reviewDetail ? t('paperTasks.reviewFlow.preparationTitle') : detail?.record.preparation ? '论文拓展' : t('paperTasks.resultTitle')} width={1000} onClose={closeDetail} closable={!busy}
                extra={detail ? <Button type="text" danger disabled={busy} onClick={confirmDeleteExpansion}>{t('paperTasks.deleteResult')}</Button> : undefined}>
                {detail && <>
                    <div className="paper-expansion-result-heading">
                        <Typography.Title level={4} ellipsis={{ rows: 2 }}>{detail.record.title}</Typography.Title>
                        {['preparing', 'awaiting_info', 'awaiting_confirmation', 'preparation_failed'].includes(detail.record.status) && <PaperTaskStatus status={detail.record.status}>{statusLabel(detail.record.status)}</PaperTaskStatus>}
                    </div>
                    {detail.chat_status !== 'available' && detail.chat_status !== 'uncreated' && <Alert type="warning" message={t(detail.chat_status === 'trashed' ? 'paperTasks.expansionTrashed' : 'paperTasks.expansionDeleted')} />}
                    <div className="paper-expansion-navigation">
                        <Button type="link" disabled={detail.chat_status !== 'available'} onClick={() => goChat(detail.record.conversation_id, reviewDetail)}>{t(reviewDetail ? 'paperTasks.reviewFlow.openChat' : 'paperTasks.overview.openChat')}</Button>
                    </div>
                    <ExpansionDetails key={`details-${detail.record.id}`} detail={detail} statusLabel={statusLabel} onRefresh={() => openDetail(detail.record.id)} onStart={id => goChat(id, detail.record.goal === 'review')}>
                        {reviewDetail && <ReviewFlow status={detail.record.status} reviewStarted={!!detail.record.auto_review_id}
                        readyCount={detail.record.result.papers.filter(item => contentReady(detail, item.paper.paper_id)).length} />}
                    </ExpansionDetails>
                    {resultReady && <GeneratedContents key={detail.record.id} items={detail.handoffs} onOpen={goChat} onReview={openReviewRecord} />}
                    {resultReady && source && <>
                    <div className="paper-expansion-papers-summary"><Typography.Text strong>{t('paperTasks.paperCount', { count: papers.length })}</Typography.Text><Typography.Text type="secondary">{t('paperTasks.downloadedCount', { count: papers.filter(item => source.contents.some(content => content.paper_id === item.paper.paper_id && content.quality?.downloaded)).length })} · {t('paperTasks.availableCount', { count: available })}</Typography.Text></div>
                    <div className="paper-expansion-result-toolbar">
                        <Tooltip title={reviewReason} trigger={['hover', 'focus']}><span tabIndex={reviewReason ? 0 : undefined}><Button type="primary" disabled={!!reviewReason} onClick={() => openConfirmation('review')}>{t('paperTasks.generateReview')}</Button></span></Tooltip>
                        <Tooltip title={bulkImportReason} trigger={['hover', 'focus']}><span tabIndex={bulkImportReason ? 0 : undefined}><Button disabled={!!bulkImportReason} onClick={() => openConfirmation('import')}>{t('paperTasks.bulkImport', { count: importable.length })}</Button></span></Tooltip>
                        {!!retryableContents.length && <Button loading={retryingContents} disabled={busy || !!confirmation || source.record.version !== detail.record.version}
                            onClick={() => void retryContents(source, retryableContents.map(item => item.paper.paper_id))}>{t('paperTasks.retryMissingContents', { count: retryableContents.length })}</Button>}
                        <span className="paper-expansion-result-selection" aria-live="polite">{t('paperTasks.selectedCount', { count: selection?.selected.length || 0 })}</span>
                    </div>

                    <p className="paper-expansion-selection-hint">{selectedPapers.length
                        ? t('paperTasks.selectedFulltextHint', { count: selectedPapers.length, ready: selectedPapers.filter(item => contentReady(source, item.paper.paper_id)).length })
                        : t('paperTasks.selectForActions')}</p>
                    {selection && detail.record.version !== selection.source.record.version && <Alert type="info" message={t('paperTasks.selectionVersion', { version: selection.source.record.version })} style={{ marginBottom: 12 }} />}
                    {actionError && !confirmation && (onNotification ? <p role="status">{actionError}</p> : <Alert type="error" showIcon message={actionError} style={{ marginBottom: 12 }} />)}
                    {canImport && preview?.status === 'error' && <Alert type="warning" message={preview.error || t('paperTasks.previewFailed')} action={<Button onClick={() => void refreshImportPreview(source)}>{t('paperTasks.retry')}</Button>} style={{ marginBottom: 12 }} />}
                    <Table<WorkflowPaper> className={`paper-expansion-result-table${selection?.selected.length ? ' has-selection' : ''}`} rowKey={(item: WorkflowPaper) => item.paper.paper_id} dataSource={papers} pagination={false} size="middle" tableLayout="fixed" scroll={{ x: 720 }}
                        expandable={{ showExpandColumn: false, expandedRowKeys: expandedPaperIds, expandedRowRender: (item: WorkflowPaper) => {
                          const reading = source.record.report?.reading?.[item.paper.paper_id] || source.record.reading?.[item.paper.paper_id];
                          const claims = source.record.report?.claims?.filter(claim => claim.paper_id === item.paper.paper_id) || [];
                          const quality = source.contents.find(content => content.paper_id === item.paper.paper_id)?.quality;
                          return <div className="paper-expansion-paper-detail" id={`${paperDetailsId}-${item.paper.paper_id}`}>
                            <div className="paper-expansion-paper-relations">
                              {relationFields.filter(field => item[field]?.trim()).map(field => <section key={field}>
                                <h5>{t(`paperTasks.paperAnalysis.${item.is_seed && field === 'seed_relation' ? 'researchOrigin' : field}`)}</h5>
                                <Typography.Paragraph>{item[field]}</Typography.Paragraph>
                              </section>)}
                            </div>
                            {item.key_contribution?.trim() && <section className="paper-expansion-paper-contribution">
                              <h5>{t('paperTasks.paperAnalysis.keyContribution')}</h5>
                              <Typography.Paragraph>{item.key_contribution}</Typography.Paragraph>
                            </section>}
                            {!relationFields.some(field => item[field]?.trim()) && !item.key_contribution?.trim() && <Typography.Text type="secondary">{t('paperTasks.workflowResult.noAnalysis')}</Typography.Text>}
                            {item.paper.abstract && <details className="paper-expansion-paper-fold">
                              <summary>{t('paperTasks.paperAnalysis.abstract')}</summary>
                              <Typography.Paragraph>{item.paper.abstract}</Typography.Paragraph>
                            </details>}
                            <details className="paper-expansion-paper-fold">
                              <summary>{t('paperTasks.paperAnalysis.evidence')}</summary>
                              <Space direction="vertical">
                                {quality && <Typography.Text type="secondary">材料检查：PDF {quality.downloaded ? '已获取' : '未获取'}；文字{quality.parsed ? '已解析' : '未解析'}；身份{quality.identity === 'matched' ? '已匹配' : quality.identity === 'mismatch' ? '不匹配' : '待核对'}；{quality.eligible ? '可用正文' : '暂不可作为正文证据'}</Typography.Text>}
                                <Typography.Text>实际读取：{reading?.read_level === 'full' ? '已读取完整正文' : reading ? '已读相关部分' : '仅题录与摘要'}</Typography.Text>
                                {reading && <Typography.Text type="secondary">已返回 {reading.positions.length} / {reading.total_blocks} 个正文块；页码：{reading.pages.filter(page => page > 0).join('、') || '原文未提供可靠页码'}</Typography.Text>}
                                {claims.map((claim, index) => <div className="paper-expansion-paper-evidence" key={index}>
                                  <Typography.Paragraph>{claim.statement}</Typography.Paragraph>
                                  <Typography.Text type="secondary">{claim.reason}</Typography.Text>
                                  {!!claim.positions.length && <div className="paper-expansion-muted">{t('paperTasks.workflowResult.positions', { positions: claim.positions.map(position => position + 1).join('、') })}</div>}
                                </div>)}
                              </Space>
                            </details>
                          </div>;
                        } }}
                        rowSelection={{ selectedRowKeys: selection?.selected || [], onChange: changeSelection, getCheckboxProps: (item: WorkflowPaper) => ({ disabled: busy || !!confirmation, 'aria-label': t('paperTasks.selectPaper', { title: item.paper.title || item.paper.paper_id }) }) }}
                        columns={[
                        { title: t('paperTasks.paper'), render: (_: unknown, item: WorkflowPaper) => {
                            const title = item.paper.title || item.paper.paper_id;
                            const expanded = expandedPaperIds.includes(item.paper.paper_id);
                            const detailsLabel = t(`paperTasks.paperAnalysis.${expanded ? 'hideDetails' : 'showDetails'}`);
                            const document = currentPaperDocument(source, item, previewItem(source, item));
                            const titleLink = (() => {
                            if (document) return <Link to={`/lib/knowledge/knowledge/${encodeURIComponent(document.dataset_id)}/${encodeURIComponent(document.document_id)}`} target="_blank" rel="noopener noreferrer">{title}</Link>;
                            if (item.is_seed) return title;
                            const { paper_url, doi, arxiv_id, pdf_url } = item.paper;
                            for (const candidate of [paper_url, doi && `https://doi.org/${doi}`, arxiv_id && `https://arxiv.org/abs/${arxiv_id}`, pdf_url]) {
                                if (!candidate) continue;
                                try {
                                    const url = new URL(candidate);
                                    if (['https:', 'http:'].includes(url.protocol) && !url.username && !url.password) {
                                        return <Typography.Link href={url.href} target="_blank" rel="noopener noreferrer">{title}</Typography.Link>;
                                    }
                                } catch {
                                    // Keep looking for a valid paper destination.
                                }
                            }
                            return title;
                            })();
                            return <div className="paper-expansion-paper-title">{titleLink}
                              <div className="paper-expansion-paper-meta">
                                {item.is_seed && <Tag color="purple" bordered={false} className="paper-expansion-seed-tag" icon={<BranchesOutlined />}>{t('paperTasks.paperAnalysis.seed')}</Tag>}
                                <span>{[item.paper.year, item.paper.venue].filter(Boolean).join(' · ')}</span>
                              </div>
                              {!item.is_seed && relationFields.filter(field => item[field]?.trim()).map(field => <div className="paper-expansion-relation-summary" key={field}>
                                <span>{t(`paperTasks.paperAnalysis.${field}Short`)}：</span><span>{item[field]}</span>
                              </div>)}
                              <Button type="text" size="small" className="paper-expansion-details-toggle"
                                aria-label={`${detailsLabel}：${title}`} aria-expanded={expanded} aria-controls={`${paperDetailsId}-${item.paper.paper_id}`}
                                onClick={() => setExpandedPaperIds(ids => ids.includes(item.paper.paper_id)
                                    ? ids.filter(id => id !== item.paper.paper_id) : [...ids, item.paper.paper_id])}>
                                {detailsLabel}<DownOutlined rotate={expanded ? 180 : 0} />
                              </Button>
                            </div>;
                        } },
                        { title: t('paperTasks.fullText'), width: 140, render: (_: unknown, item: WorkflowPaper) => {
                            const content = source.contents.find(entry => entry.paper_id === item.paper.paper_id);
                            return <Space direction="vertical"><span>{contentStatus(source, item.paper.paper_id)}</span>
                                {content?.error && <Tooltip title={content.error} trigger={['hover', 'focus']}><Typography.Text type="secondary" tabIndex={0}>{t('paperTasks.contentReason')}</Typography.Text></Tooltip>}
                                {retryableContents.some(paper => paper.paper.paper_id === item.paper.paper_id) && <Button disabled={retryingContents || source.record.version !== detail.record.version}
                                    onClick={() => void retryContents(source, [item.paper.paper_id])}>{t(content ? 'paperTasks.retryContent' : 'paperTasks.fetchContent')}</Button>}
                            </Space>;
                        } },
                        { title: t('paperTasks.importState'), width: 155, render: (_: unknown, item: WorkflowPaper) => {
                            const entry = previewItem(source, item);
                            const presence = detail.presence?.find(value => value.work_id === item.paper.paper_id);
                            const present = currentPaperDocument(source, item, entry) || entry?.disposition === 'already_present';
                            const inProgress = importing(source, item);
                            const reason = importReason(source, item);
                            const status = present ? t('paperTasks.inCurrentCollection') : inProgress ? t('paperTasks.importStatus.importing')
                                : presence?.current_dataset.length ? t('paperTasks.otherVersion') : presence?.metadata_only ? t('paperTasks.bibliographyOnly') : t('paperTasks.importStatus.not_imported');
                            return <Space direction="vertical"><span>{status}</span>{!present && <Tooltip title={reason}><span><Button size="small" disabled={busy || !!confirmation || !!reason}
                                aria-label={t('paperTasks.importPaper', { title: item.paper.title || item.paper.paper_id })}
                                onClick={() => importPaper(source, item)}>{t(inProgress ? 'paperTasks.importStatus.importing' : 'paperTasks.import')}</Button></span></Tooltip>}</Space>;
                        } },
                    ]} />
                    </>}
                </>}
                <Modal open={resultReady && !!confirmation} width={640} className="paper-expansion-confirmation" title={t(confirmation?.kind === 'review' ? 'paperTasks.reviewConfirmTitle' : 'paperTasks.bulkImportTitle')}
                    onCancel={() => !busy && !topicBusy && setConfirmation(undefined)} onOk={() => confirmation?.kind === 'review' ? void submitReview() : confirmation && void submitImport(confirmation.source, confirmationEligible, confirmation.requestKey)}
                    cancelText={t('paperTasks.cancel')} confirmLoading={busy} closable={!busy && !topicBusy} maskClosable={!busy && !topicBusy} keyboard={!busy && !topicBusy}
                    okText={t(confirmation?.kind === 'review' ? 'paperTasks.confirmReview' : 'paperTasks.confirmImport', { count: confirmationEligible.length })}
                    okButtonProps={{ disabled: !!confirmationReason || topicBusy }}>
                    {confirmation?.kind === 'review' && <Typography.Paragraph type="secondary">{t('paperTasks.newReviewHint')}</Typography.Paragraph>}
                    <Alert type="info" message={t(confirmation?.kind === 'review' ? 'paperTasks.reviewSummary' : 'paperTasks.importSummary', { count: confirmationEligible.length, selected: confirmationPapers.length, skipped: confirmationPapers.length - confirmationEligible.length })} />
                    {actionError && (onNotification ? <p role="status">{actionError}</p> : <Alert type="error" showIcon message={actionError} style={{ marginTop: 12 }} />)}
                    <List dataSource={[...confirmationEligible, ...confirmationPapers.filter(item => !confirmationEligible.includes(item))]} locale={{ emptyText: t('paperTasks.selectPaperFirst') }} renderItem={item => {
                        const eligible = confirmationEligible.includes(item);
                        return <List.Item className={eligible ? '' : 'is-excluded'}><List.Item.Meta
                            avatar={eligible ? <CheckOutlined className="paper-expansion-confirm-check" /> : <MinusOutlined />}
                            title={item.paper.title || item.paper.paper_id}
                            description={confirmation?.kind === 'review' ? eligible ? t('paperTasks.reviewIncluded') : `${contentStatus(confirmation.source, item.paper.paper_id)} · ${t('paperTasks.reviewExcluded')}` : confirmation && (importReason(confirmation.source, item) || t('paperTasks.canImport'))} /></List.Item>;
                    }} />
                    {confirmation?.kind === 'review' && <>
                        <ReviewTopic value={reviewTopic} onChange={setReviewTopic} disabled={busy || !confirmationEligible.length} onBusyChange={setTopicBusy}
                            input={{ dataset_id: confirmation.source.record.dataset_id, expansion_id: confirmation.source.record.id, expansion_version: confirmation.source.record.version, paper_ids: confirmationEligible.map(item => item.paper.paper_id), user_instruction: instruction }} />
                        <label className="paper-review-field">{t('paperTasks.additional')}<Input.TextArea value={instruction} disabled={busy || topicBusy} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setInstruction(event.target.value)} /></label>
                    </>}
                    <Typography.Paragraph type="secondary">{confirmationReason || t(confirmation?.kind === 'review' ? 'paperTasks.reviewConfirmHint' : 'paperTasks.importConfirmHint', { count: confirmationEligible.length })}</Typography.Paragraph>
                </Modal>
            </Drawer>
        </Drawer>
    </>;
}
