import { useCallback, useEffect, useRef, useState, type ChangeEvent, type Key } from 'react';
import { createPortal } from 'react-dom';
import { Alert, Button, Checkbox, Drawer, Dropdown, Input, List, Modal, Select, Space, Table, Tag, Tooltip, Typography, message } from 'antd';
import type { CheckboxChangeEvent } from 'antd/es/checkbox';
import { BranchesOutlined, DownOutlined, EditOutlined, FundProjectionScreenOutlined, HistoryOutlined, LoadingOutlined, ReadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { getChatConversationPath, CHAT_CONVERSATION_LIST_REFRESH_EVENT } from '@/modules/chat/constants/chat';
import { deleteExpansion, getExpansion, launchPaperTask, listExpansions, resolvePaperSelection, retryPaperContent, type Expansion, type ExpansionDetail, type PaperTask, type WorkflowPaper } from '../../api/paperWorkflows';
import { previewPaperImport, startReferenceImport, getPaperImportStatus, type ImportPreviewItem, isImportPreviewExpired } from '../../api/academicReferences';
import './index.scss';
import MarkdownViewer from '../MarkdownViewer';
import PreparationPanel, { budgetOptions } from './PreparationPanel';
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
interface ImportPreview {
    sourceKey: string;
    items: ImportPreviewItem[];
    status: 'loading' | 'ready' | 'error';
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
function currentPaperDocument(detail: ExpansionDetail, item: WorkflowPaper, preview?: ImportPreviewItem) {
    const documents = [...(detail.presence?.find(entry => entry.work_id === item.paper.paper_id)?.current_dataset || []), ...(preview?.presence.current_dataset || [])];
    return documents.find(document => document.dataset_id === detail.record.dataset_id && document.document_id && (
        item.paper.document_id ? document.document_id === item.paper.document_id : item.paper.source_version
            ? document.source_version === item.paper.source_version && document.source_provider === item.paper.source_provider
            : item.is_seed
    ));
}
const stages = ['expansion_plan', 'candidate_set', 'relation_analysis', 'expansion_result'];

export default function PaperTasks({ dataset, selected, selectedTitles = [], selectionError, canImport, onImportSubmitted, actionsContainer, onNotification, onNotificationResolved }: Props) {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const [params, setParams] = useSearchParams();
    const [launch, setLaunch] = useState<{ kind: PaperTask; direction: boolean }>();
    const [instruction, setInstruction] = useState('');
    const [reflectionFirst, setReflectionFirst] = useState(false);
    const [budgetMode, setBudgetMode] = useState<BudgetMode>('balanced');
    const [capabilities, setCapabilities] = useState<{openalex_key: boolean; sciverse: boolean; web_search: boolean}>();
    useEffect(() => { if (launch) void getExpansionCapabilities().then(setCapabilities).catch(() => setCapabilities(undefined)); }, [launch]);
    const [busy, setBusy] = useState(false);
    const [history, setHistory] = useState(false);
    const [historyError, setHistoryError] = useState(false);
    const [records, setRecords] = useState<Expansion[]>([]);
    const [detail, setDetail] = useState<ExpansionDetail>();
    const [importBatches, setImportBatches] = useState<{ id: string; keys: string[] }[]>([]);
    const importsChanged = useRef(onImportSubmitted);
    importsChanged.current = onImportSubmitted;
    const [selection, setSelection] = useState<ResultSelection>();
    const [confirmation, setConfirmation] = useState<ResultSelection>();
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
        if (!direction) {
            if (selectedPaperTaskError) { message.warning(selectedPaperTaskError); return; }
        }
        setActionError('');
        setInstruction('');
        setReflectionFirst(false);
        setLaunch({ kind, direction });
        requestKey.current = crypto.randomUUID();
        requestInput.current = '';
    }, [selectedPaperTaskError]);
    const refreshHistory = async () => {
        try {
            setRecords(await listExpansions(dataset, !!onNotification));
            setHistoryError(false);
            onNotificationResolved?.('expansion-history');
        } catch (error) {
            setHistoryError(true);
            onNotification?.({ id: 'expansion-history', title: '论文扩展记录加载失败', message: error instanceof Error ? error.message : '记录加载失败，请重试', action: { label: '重新加载', onClick: refreshHistory } });
        }
    };
    const openHistory = () => {
        setHistory(true);
        void refreshHistory().catch(() => {});
    };
    const openDetail = async (id: string) => {
        if (detailTarget.current !== id) {
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
                setDetail(result);
                setParams(current => { if (current.get('expansion_id') === id) return current; const next = new URLSearchParams(current); next.set('expansion_id', id); return next; }, { replace: true });
                onNotificationResolved?.(`expansion-detail:${id}`);
                for (const content of result.contents) {
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
    const expanding = !!detail && ['running', 'active'].includes(detail.record.status);
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
        if (!launch || busy) return;
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
            if (!launch.direction) {
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
                user_instruction: launch.direction ? undefined : instruction.trim(), reflection_first: reflectionFirst, budget_mode: budgetMode,
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
        if (content?.status === 'needs_review') return '材料待核对';
        return t(`paperTasks.content.${content?.status === 'ready' && !content.content_hash ? 'pending' : content?.status || 'pending'}`);
    };
    // Keep the selected result snapshot stable while polling live content and import status.
    const source = detail && selection?.source.record.id === detail.record.id
        ? (detail.record.version === selection.source.record.version ? { ...detail, record: selection.source.record } : selection.source) : detail;
    const papers = source ? distinctPapers(source.record.result.papers || []) : [];
    const refreshImportPreview = async (target: ExpansionDetail) => {
        if (!canImport) return;
        const sourceKey = resultKey(target);
        const ids = distinctPapers(target.record.result.papers || []).map(item => item.paper.paper_id);
        const request = ++previewRequest.current;
        setPreview(current => current?.sourceKey === sourceKey && current.status === 'ready'
            ? current : { sourceKey, items: [], status: 'loading' });
        try {
            const key = `${sourceKey}:${ids.join(',')}`;
            const pending = pendingPreview.current?.key === key ? pendingPreview.current : { key, promise: ids.length ? previewPaperImport(ids, target.record.dataset_id, target.record.id, !!onNotification) : Promise.resolve([]) };
            pendingPreview.current = pending;
            let items: ImportPreviewItem[];
            try { items = await pending.promise; }
            finally { if (pendingPreview.current === pending) pendingPreview.current = undefined; }
            if (request === previewRequest.current) { setPreview({ sourceKey, items, status: 'ready' }); onNotificationResolved?.(`expansion-preview:${target.record.id}`); }
        } catch (error) {
            if (request === previewRequest.current) {
                setPreview({ sourceKey, items: [], status: 'error' });
                onNotification?.({ id: `expansion-preview:${target.record.id}`, title: '论文导入预览加载失败', message: error instanceof Error ? error.message : '导入预览加载失败', action: { label: '重新加载预览', onClick: () => refreshImportPreview(target) } });
            }
        }
    };
    useEffect(() => {
        if (source) void refreshImportPreview(source);
    }, [source ? resultKey(source) : '', JSON.stringify(papers.map(item => [item.paper.paper_id, item.paper.source_version])), JSON.stringify(detail?.presence), canImport]);
    useEffect(() => {
        const onFocus = () => { if (source) void refreshImportPreview(source); };
        window.addEventListener('focus', onFocus);
        return () => window.removeEventListener('focus', onFocus);
    }, [source ? resultKey(source) : '', canImport]);
    const previewMatches = (item: WorkflowPaper, entry?: ImportPreviewItem) => !item.paper.source_version ||
        (entry?.paper?.source_version === item.paper.source_version && entry.paper.source_provider === item.paper.source_provider);
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
        if (preview.status === 'error') return t('paperTasks.previewFailed');
        if (!previewMatches(item, preview.items.find(value => value.work_id === item.paper.paper_id))) return t('paperTasks.importVersionChanged');
        return entry?.disposition === 'downloadable' && entry.fulltext_candidates.length ? '' : t('paperTasks.importStatus.metadata_only');
    };
    const changeSelection = (keys: Key[]) => {
        if (!source || busy || confirmation) return;
        setSelection(current => keys.length ? { source: current?.source || source, selected: keys.map(String), requestKey: crypto.randomUUID() } : undefined);
        setActionError('');
    };
    const selectedPapers = papers.filter(item => selection?.selected.includes(item.paper.paper_id));
    const importable = source ? selectedPapers.filter(item => !importReason(source, item)) : [];
    const actionReason = busy ? t('paperTasks.submitting') : confirmation ? t('paperTasks.finishImportConfirmation') : '';
    const reviewReason = actionReason || (!source?.record.version ? t('paperTasks.resultPending')
        : !papers.length ? t('paperTasks.noPapersYet')
        : selectedPapers.length < 2 ? t('paperTasks.reviewMinimum')
        : selectedPapers.some(item => !contentReady(source, item.paper.paper_id)) ? t('paperTasks.reviewFulltextRequired') : '');
    const bulkImportReason = actionReason || (!canImport ? t('paperTasks.importForbidden')
        : !papers.length ? t('paperTasks.noPapersYet')
        : !selectedPapers.length ? t('paperTasks.noImportableSelection')
        : !importable.length && source ? t('paperTasks.selectionNotImportable', { reasons: [...new Set(selectedPapers.map(item => importReason(source, item)))].join('；') }) : '');
    const submitReview = async () => {
        if (!source || !selection || reviewReason || submitting.current || busy) return;
        submitting.current = 'review';
        setBusy(true);
        setActionError('');
        try {
            const result = await launchPaperTask({
                kind: 'review', dataset_id: source.record.dataset_id,
                expansion_id: source.record.id, expansion_version: source.record.version,
                paper_ids: selection.selected, request_key: selection.requestKey,
            }, !!onNotification);
            onNotificationResolved?.(`expansion-review:${source.record.id}`);
            if (!result.conversation_id) throw new Error('任务未返回 Chat，请重试');
            goChat(result.conversation_id);
        } catch (error) {
            setActionError(onNotification ? '提交尚未完成，请重试或查看通知。' : t('paperTasks.submitFailed'));
            onNotification?.({ id: `expansion-review:${source.record.id}`, title: '论文综述任务创建失败', message: error instanceof Error ? error.message : '任务创建失败' });
        } finally {
            submitting.current = undefined;
            setBusy(false);
        }
    };
    const submitImport = async (target: ExpansionDetail, chosen: WorkflowPaper[], key: string) => {
        if (!canImport || !chosen.length || chosen.some(item => importReason(target, item)) || submitting.current || busy) return;
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
        const fingerprint = importKey(target, item);
        let key = importRequestKeys.current.get(fingerprint);
        if (!key) { key = crypto.randomUUID(); importRequestKeys.current.set(fingerprint, key); }
        void submitImport(target, [item], key);
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
    const confirmationImportable = confirmation ? confirmationPapers.filter(item => !importReason(confirmation.source, item)) : [];
    const sourceLabel = (item: WorkflowPaper) => t(item.is_seed ? 'paperTasks.seed' : 'paperTasks.expanded');
    const activeTask = launch?.kind;
    const actions = <div className="paper-library-task-actions" role="group" aria-label="论文工具">
            <Dropdown trigger={['click']} placement="topLeft" menu={{ items: [
                { key: 'direction', label: t('paperTasks.expandDirection'), onClick: () => openLaunch('expansion', true) },
                { type: 'divider' },
                { key: 'papers', className: 'paper-library-task-selection-action', label: <Tooltip title={selectedPaperTaskError || undefined} placement="right"><span className="paper-library-task-selection-label">{t('paperTasks.expandPapers')}</span></Tooltip>, disabled: !!selectedPaperTaskError, onClick: () => openLaunch('expansion', false) },
            ] }}><Button type="text" className={`paper-library-task-action ${activeTask === 'expansion' ? 'is-active' : ''}`} icon={<BranchesOutlined aria-hidden />} aria-pressed={activeTask === 'expansion'}>{taskLabel('expansion')} <DownOutlined aria-hidden /></Button></Dropdown>
            <Dropdown trigger={['click']} placement="topLeft" menu={{ items: [
                { key: 'history', label: t('paperTasks.reviewHistory'), onClick: openHistory },
                { key: 'direction', label: t('paperTasks.reviewDirection'), onClick: () => openLaunch('review', true) },
                { type: 'divider' },
                { key: 'papers', className: 'paper-library-task-selection-action', label: <Tooltip title={selectedPaperTaskError || undefined} placement="right"><span className="paper-library-task-selection-label">{t('paperTasks.reviewPapers')}</span></Tooltip>, disabled: !!selectedPaperTaskError, onClick: () => openLaunch('review', false) },
            ] }}><Button type="text" className={`paper-library-task-action ${activeTask === 'review' ? 'is-active' : ''}`} icon={<ReadOutlined aria-hidden />} aria-pressed={activeTask === 'review'}>{taskLabel('review')} <DownOutlined aria-hidden /></Button></Dropdown>
            <Tooltip title={selectedPaperTaskError || undefined} placement="right"><span><Button type="text" className={`paper-library-task-action ${activeTask === 'reflection' ? 'is-active' : ''}`} icon={<EditOutlined aria-hidden />} aria-pressed={activeTask === 'reflection'} disabled={!!selectedPaperTaskError} onClick={() => openLaunch('reflection', false)}>{taskLabel('reflection')}</Button></span></Tooltip>
            <Tooltip title={selectedPaperTaskError || undefined} placement="right"><span><Button type="text" className={`paper-library-task-action ${activeTask === 'ppt' ? 'is-active' : ''}`} icon={<FundProjectionScreenOutlined aria-hidden />} aria-pressed={activeTask === 'ppt'} disabled={!!selectedPaperTaskError} onClick={() => openLaunch('ppt', false)}>{taskLabel('ppt')}</Button></span></Tooltip>
            <Button type="text" className="paper-library-task-action" icon={<HistoryOutlined aria-hidden />} onClick={openHistory}>{t('paperTasks.records')}</Button>
        </div>;
    return <>
        {actionsContainer ? createPortal(actions, actionsContainer) : actions}
        <Modal okText={launch && ['expansion', 'review'].includes(launch.kind) ? '开始初步调研' : t('paperTasks.start')} cancelText={t('paperTasks.cancel')} open={!!launch}
            title={launch ? taskLabel(launch.kind) : ''}
            onCancel={() => !busy && setLaunch(undefined)} onOk={() => void submit()} confirmLoading={busy}>
            {actionError && onNotification && <p role="status">{actionError}</p>}
            {launch?.kind === 'review' && <Alert type="info" showIcon message={t('paperTasks.reviewStartHint')} />}
            {launch && !launch.direction && <div className="paper-task-launch-selection">
                <Typography.Text type="secondary">{t('paperTasks.selectedLibraryPapers', { count: selected.length })}</Typography.Text>
                {selectedTitles.length > 0 && <Typography.Paragraph type="secondary" ellipsis={{ rows: 2, tooltip: true }}>{selectedTitles.join(' · ')}</Typography.Paragraph>}
            </div>}
            <Typography.Paragraph style={{ marginTop: 12, marginBottom: 4 }}>{t(launch?.direction ? 'paperTasks.topic' : 'paperTasks.additional')}</Typography.Paragraph>
            <Input.TextArea aria-label={t(launch?.direction ? 'paperTasks.topic' : 'paperTasks.additional')}
                placeholder={t(launch?.direction ? 'paperTasks.topicPlaceholder' : `paperTasks.placeholder.${launch?.kind || 'expansion'}`)}
                value={instruction} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setInstruction(event.target.value)} />
            {launch && ['expansion', 'review'].includes(launch.kind) && <Space direction="vertical" style={{ width: '100%', marginTop: 12 }}>
                <Select aria-label="执行预算" value={budgetMode} options={budgetOptions} onChange={setBudgetMode} style={{ width: '100%' }} />
                {capabilities && (!capabilities.openalex_key || !capabilities.sciverse) && <Alert type="info" message="可使用当前配置开始调研" description={<>
                  {!capabilities.openalex_key && <div>OpenAlex 使用基础访问，部分检索深度与托管全文受限。<Link to="/settings?section=knowledge&tool=academic-search&editor=service&item=openalex">配置 OpenAlex</Link></div>}
                  {!capabilities.sciverse && <div>Sciverse 检索未启用。<Link to="/settings?section=knowledge&tool=academic-search&editor=service&item=sciverse">配置 Sciverse</Link></div>}
                </>} />}
            </Space>}
            {launch?.kind === 'ppt' && <Checkbox style={{ marginTop: 12 }} checked={reflectionFirst} onChange={(event: CheckboxChangeEvent) => setReflectionFirst(event.target.checked)}>{t('paperTasks.reflectionFirst')}</Checkbox>}
        </Modal>
        <Drawer open={history} title={t('paperTasks.personalRecords')} width={780} onClose={() => setHistory(false)}>
            <Button onClick={() => void refreshHistory()}>{t('paperTasks.refresh')}</Button>
            <List dataSource={records} locale={{ emptyText: historyError ? '记录暂时无法加载，请重试。' : t('paperTasks.noRecords') }} renderItem={record => <List.Item actions={[
                <Button key="view" onClick={() => void openDetail(record.id).catch(() => {})}>{t('paperTasks.viewResult')}</Button>,
            ]}><List.Item.Meta title={record.title} description={`${statusLabel(record.status)} · ${t('paperTasks.paperCount', { count: distinctPapers(record.result?.papers || []).length })}`} /></List.Item>} />
        </Drawer>
        <Drawer open={!!detail} title={t('paperTasks.resultTitle')} width={1000} onClose={closeDetail} closable={!busy}>
            {detail && <>
                <div className="paper-expansion-result-heading">
                    <Typography.Title level={4} ellipsis={{ rows: 2 }}>{detail.record.title}</Typography.Title>
                    <Tag role="status" color={expanding ? 'processing' : detail.record.status === 'failed' ? 'error' : detail.record.status === 'completed' ? 'success' : undefined} icon={expanding ? <LoadingOutlined /> : undefined}>
                        {expanding ? t('paperTasks.expanding') : statusLabel(detail.record.status)}
                    </Tag>
                </div>
                {detail.chat_status !== 'available' && detail.chat_status !== 'uncreated' && <Alert type="warning" message={t(detail.chat_status === 'trashed' ? 'paperTasks.expansionTrashed' : 'paperTasks.expansionDeleted')} />}
                <PreparationPanel detail={detail} onRefresh={() => openDetail(detail.record.id)} onStart={id => goChat(id, detail.record.goal === 'review')} />
                {detail.record.report?.narrative && <section aria-label="研究脉络与阅读建议">
                  <Typography.Title level={5}>研究脉络与阅读建议</Typography.Title>
                  <MarkdownViewer>{detail.record.report.narrative}</MarkdownViewer>
                  {detail.record.report.limitations?.map((item, index) => <Typography.Paragraph key={index} type="secondary">{item}</Typography.Paragraph>)}
                  <Typography.Text type="secondary">货币成本：{detail.record.report.currency_cost == null ? '未知' : detail.record.report.currency_cost}</Typography.Text>
                  {detail.record.report.execution_elapsed_ms != null && <Typography.Paragraph>本轮正式执行：{Math.round(detail.record.report.execution_elapsed_ms / 1000)} 秒</Typography.Paragraph>}
                  {detail.record.report.stage_timing?.map((stage, index) => <div key={index}>{({prepare_plan: '准备检索计划', expand_and_screen: '拓展与筛选', analyze_relations: '分析论文关系', compose_result: '研究脉络与阅读建议'} as Record<string, string>)[stage.step_id] || stage.step_id} · 第 {stage.attempt} 次 · {Math.round(stage.queue_and_execution_ms / 1000)} 秒 · {stage.status}</div>)}
                  {detail.record.report.source_queries?.map((query, index) => <div key={index}>{query.query}：{query.sources?.map(source => `${source.source} ${source.status}（${source.count} 篇，${Math.round(source.elapsed_ms / 1000)} 秒）`).join('；')}</div>)}
                  <details><summary>模型与上游用量记录</summary><pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{JSON.stringify({ preparation: detail.record.report.preparation_attempts, stages: detail.record.report.stage_metrics, composition: detail.record.report.compose_metrics }, null, 2)}</pre></details>
                </section>}
                <Space wrap style={{ margin: '12px 0' }}>
                    <Button disabled={detail.chat_status !== 'available'} onClick={() => goChat(detail.record.conversation_id)}>{t('paperTasks.returnExpansion')}</Button>
                    <Button danger onClick={() => Modal.confirm({
                        title: t('paperTasks.deleteTitle'), content: t('paperTasks.deleteHint'), okText: t('paperTasks.confirmDelete'), cancelText: t('paperTasks.cancel'), okButtonProps: { danger: true },
                        onOk: async () => {
                            try { await deleteExpansion(detail.record.id, !!onNotification); onNotificationResolved?.(`expansion-delete:${detail.record.id}`); closeDetail(); await refreshHistory(); }
                            catch (error) { onNotification?.({ id: `expansion-delete:${detail.record.id}`, title: '论文扩展记录删除失败', message: error instanceof Error ? error.message : '删除失败，请重试' }); throw error; }
                        },
                    })}>{t('paperTasks.deleteResult')}</Button>
                </Space>
                <Typography.Paragraph ellipsis={{ rows: 3, expandable: true, symbol: t('paperTasks.expand') }} style={{ whiteSpace: 'pre-wrap', marginTop: 12 }}>{detail.record.result.user_instruction}</Typography.Paragraph>
                <Typography.Paragraph>{t('paperTasks.directions', { value: detail.record.result.selected_directions?.join('；') || t('paperTasks.undetermined') })}</Typography.Paragraph>
                {source && <>
                <div className="paper-expansion-result-toolbar">
                    <div className="paper-expansion-result-selection">
                        <span aria-live="polite">{t('paperTasks.selectedCount', { count: selection?.selected.length || 0 })}</span>
                        {papers.length > 0 && <span>{t('paperTasks.availableCount', { count: available })}</span>}
                        {!!selection?.selected.length && <Button type="text" size="small" disabled={busy || !!confirmation} onClick={cancelSelection}>{t('paperTasks.clearSelection')}</Button>}
                    </div>
                    <Space wrap>
                        <Tooltip title={reviewReason} trigger={['hover', 'focus']}><span tabIndex={reviewReason ? 0 : undefined}><Button type="primary" disabled={!!reviewReason} loading={submitting.current === 'review'} onClick={() => void submitReview()}>{t('paperTasks.generateReview')}</Button></span></Tooltip>
                        <Tooltip title={bulkImportReason} trigger={['hover', 'focus']}><span tabIndex={bulkImportReason ? 0 : undefined}><Button disabled={!!bulkImportReason} onClick={() => { if (selection) { setActionError(''); setConfirmation({ ...selection, source }); } }}>{t('paperTasks.bulkImport', { count: importable.length })}</Button></span></Tooltip>
                    </Space>
                </div>
                {selection && detail.record.version !== selection.source.record.version && <Alert type="info" message={t('paperTasks.selectionVersion', { version: selection.source.record.version })} style={{ marginBottom: 12 }} />}
                {actionError && !confirmation && (onNotification ? <p role="status">{actionError}</p> : <Alert type="error" showIcon message={actionError} style={{ marginBottom: 12 }} />)}
                {canImport && preview?.status === 'error' && <Alert type="warning" message={t('paperTasks.previewFailed')} action={<Button onClick={() => void refreshImportPreview(source)}>{t('paperTasks.retry')}</Button>} style={{ marginBottom: 12 }} />}
                <Table<WorkflowPaper> className={`paper-expansion-result-table${selection?.selected.length ? ' has-selection' : ''}`} rowKey={(item: WorkflowPaper) => item.paper.paper_id} dataSource={papers} pagination={false} scroll={{ x: 960 }}
                    expandable={{ expandedRowRender: (item: WorkflowPaper) => {
                      const reading = source.record.report?.reading?.[item.paper.paper_id] || source.record.reading?.[item.paper.paper_id];
                      const claims = source.record.report?.claims?.filter(claim => claim.paper_id === item.paper.paper_id) || [];
                      return <Space direction="vertical">
                        <Typography.Text>实际读取：{reading?.read_level === 'full' ? '已读取完整正文' : reading ? '已读相关部分' : '仅题录与摘要'}</Typography.Text>
                        {reading && <Typography.Text type="secondary">已返回 {reading.positions.length} / {reading.total_blocks} 个正文块；页码：{reading.pages.filter(page => page > 0).join('、') || '原文未提供可靠页码'}</Typography.Text>}
                        {item.paper.abstract && <Typography.Paragraph>{item.paper.abstract}</Typography.Paragraph>}
                        {claims.map((claim, index) => <Typography.Paragraph key={index}>[{claim.status}] {claim.statement}：{claim.reason}（正文块 {claim.positions.join('、')}）</Typography.Paragraph>)}
                      </Space>;
                    } }}
                    rowSelection={{ selectedRowKeys: selection?.selected || [], onChange: changeSelection, getCheckboxProps: (item: WorkflowPaper) => ({ disabled: busy || !!confirmation, 'aria-label': t('paperTasks.selectPaper', { title: item.paper.title || item.paper.paper_id }) }) }}
                    columns={[
                    { title: t('paperTasks.paper'), width: 280, render: (_: unknown, item: WorkflowPaper) => {
                        const title = item.paper.title || item.paper.paper_id;
                        const document = currentPaperDocument(source, item, previewItem(source, item));
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
                    } },
                    { title: t('paperTasks.source'), width: 80, render: (_: unknown, item: WorkflowPaper) => sourceLabel(item) },
                    { title: t('paperTasks.fullText'), width: 140, render: (_: unknown, item: WorkflowPaper) => {
                        const content = source.contents.find(entry => entry.paper_id === item.paper.paper_id);
                        return <Space direction="vertical"><span>{contentStatus(source, item.paper.paper_id)}</span>
                            {content?.error && <Typography.Text type={onNotification ? 'secondary' : 'danger'}>{onNotification ? '全文尚未获取，详情见通知。' : content.error}</Typography.Text>}
                            {(content?.status === 'failed' || content?.status === 'unavailable' || content?.status === 'needs_review') && <Button disabled={source.record.version !== detail.record.version} onClick={async () => {
                                try { await retryPaperContent(item.paper.paper_id, detail.record.id, !!onNotification); onNotificationResolved?.(`content-retry:${item.paper.paper_id}`); await openDetail(detail.record.id); }
                                catch (error) { onNotification?.({ id: `content-retry:${item.paper.paper_id}`, title: `《${item.paper.title || item.paper.paper_id}》全文重试失败`, message: error instanceof Error ? error.message : "重试失败" }); }
                            }}>{t('paperTasks.retryContent')}</Button>}
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
                    { title: t('paperTasks.relations'), render: (_: unknown, item: WorkflowPaper) => <Typography.Paragraph ellipsis={{ rows: 3, expandable: true, symbol: t('paperTasks.expand') }}>{[item.seed_relation, item.direction_relation, item.key_contribution].filter(Boolean).join('；')}</Typography.Paragraph> },
                ]} />
                </>}
                <Typography.Title level={5}>{t('paperTasks.progress')}</Typography.Title>
                <List dataSource={Object.entries(detail.record.progress || {}).filter(([key, value]) => stages.includes(key) && value && typeof value === 'object').sort(([a], [b]) => stages.indexOf(a) - stages.indexOf(b))}
                    renderItem={([key, value]) => <List.Item><List.Item.Meta title={t(`paperTasks.stage.${key}`)} description={<><div>{value.candidate_directions?.join('；')}</div><div>{value.papers?.map(item => item.paper.title).filter(Boolean).join('；')}</div></>} /></List.Item>} />
                <Typography.Title level={5}>{t('paperTasks.rounds')}</Typography.Title>
                <List dataSource={detail.rounds} renderItem={round => <List.Item><details>
                    <summary>{t('paperTasks.round', { count: round.version })} · {statusLabel(round.status)} · {t('paperTasks.paperCount', { count: distinctPapers(round.result.papers || []).length })}</summary>
                    <Typography.Paragraph style={{ whiteSpace: 'pre-wrap' }}>{round.result.user_instruction}</Typography.Paragraph>
                    <Typography.Paragraph>{t('paperTasks.directions', { value: round.result.selected_directions?.join('；') })}</Typography.Paragraph>
                    <ul>{distinctPapers(round.result.papers || []).map(item => <li key={item.paper.paper_id}>{item.paper.title || item.paper.paper_id} <Tag>{sourceLabel(item)}</Tag></li>)}</ul>
                </details></List.Item>} />
                <Typography.Title level={5}>{t('paperTasks.generated')}</Typography.Title>
                <List dataSource={detail.handoffs} renderItem={item => <List.Item><Button onClick={() => goChat(item.conversation_id)}>{t('paperTasks.viewTask', { task: taskLabel(item.kind) })}</Button></List.Item>} />
            </>}
        </Drawer>
        <Modal open={!!confirmation} width={720} title={t('paperTasks.bulkImportTitle')}
            onCancel={() => !busy && setConfirmation(undefined)} onOk={() => confirmation && void submitImport(confirmation.source, confirmationImportable, confirmation.requestKey)}
            cancelText={t('paperTasks.cancel')} confirmLoading={busy} okText={t('paperTasks.confirmImport', { count: confirmationImportable.length })}
            okButtonProps={{ disabled: !confirmationImportable.length || !canImport }}>
            <Alert type="info" message={t('paperTasks.importSummary', { count: confirmationImportable.length, skipped: confirmationPapers.length - confirmationImportable.length })} />
            {actionError && (onNotification ? <p role="status">{actionError}</p> : <Alert type="error" showIcon message={actionError} style={{ marginTop: 12 }} />)}
            <List dataSource={confirmationPapers} renderItem={item => <List.Item><List.Item.Meta title={item.paper.title || item.paper.paper_id}
                description={confirmation && (importReason(confirmation.source, item) || t('paperTasks.canImport'))} /></List.Item>} />
        </Modal>
    </>;
}
