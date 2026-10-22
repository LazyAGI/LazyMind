import { useEffect, useRef, useState, type ChangeEvent, type Key } from 'react';
import { Alert, Button, Checkbox, Drawer, Dropdown, Input, List, Modal, Space, Table, Tag, Typography, message } from 'antd';
import type { CheckboxChangeEvent } from 'antd/es/checkbox';
import { DownOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { getChatConversationPath, CHAT_CONVERSATION_LIST_REFRESH_EVENT } from '@/modules/chat/constants/chat';
import { deleteExpansion, getExpansion, launchPaperTask, listExpansions, resolvePaperSelection, retryPaperContent, type Expansion, type ExpansionDetail, type PaperTask, type WorkflowPaper } from '../../api/paperWorkflows';
import { previewPaperImport, startReferenceImport, getPaperImportStatus, type ImportPreviewItem } from '../../api/academicReferences';

interface Props {
    dataset: string;
    mode: PaperTask | undefined;
    setMode: (mode: PaperTask | undefined) => void;
    selected: string[];
    canImport: boolean;
    onImportSubmitted: () => void;
}
interface ResultOperation {
    kind: 'review' | 'import';
    source: ExpansionDetail;
    selected: string[];
    preview: ImportPreviewItem[];
    previewStatus: 'loading' | 'ready' | 'error';
    requestKey: string;
    error?: string;
}
function distinctPapers(papers: WorkflowPaper[]) {
    const result = new Map<string, WorkflowPaper>();
    for (const paper of papers) {
        const previous = result.get(paper.paper.paper_id);
        result.set(paper.paper.paper_id, { ...paper, is_seed: paper.is_seed || !!previous?.is_seed });
    }
    return [...result.values()];
}
const stages = ['direction_research', 'seed_set', 'expanded_papers', 'paper_relations', 'expansion_result'];

export default function PaperTasks({ dataset, mode, setMode, selected, canImport, onImportSubmitted }: Props) {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const [params, setParams] = useSearchParams();
    const [launch, setLaunch] = useState<{ kind: PaperTask; direction: boolean }>();
    const [instruction, setInstruction] = useState('');
    const [reflectionFirst, setReflectionFirst] = useState(false);
    const [busy, setBusy] = useState(false);
    const [history, setHistory] = useState(false);
    const [records, setRecords] = useState<Expansion[]>([]);
    const [detail, setDetail] = useState<ExpansionDetail>();
    const [importBatches, setImportBatches] = useState<string[]>([]);
    const importsChanged = useRef(onImportSubmitted);
    importsChanged.current = onImportSubmitted;
    const [operation, setOperation] = useState<ResultOperation>();
    const requestKey = useRef('');
    const requestInput = useRef('');
    const previewRequest = useRef(0);
    const detailRequest = useRef(0);
    const detailTarget = useRef<string>();
    const taskLabel = (kind: string) => t(`paperTasks.task.${kind}`, { defaultValue: kind });
    const statusLabel = (status: string) => t(`paperTasks.status.${status}`, { defaultValue: status });
    const openLaunch = (kind: PaperTask, direction: boolean) => {
        setInstruction('');
        setReflectionFirst(false);
        setLaunch({ kind, direction });
        requestKey.current = crypto.randomUUID();
        requestInput.current = '';
    };
    const refreshHistory = async () => setRecords(await listExpansions(dataset));
    const openHistory = () => {
        setHistory(true);
        void refreshHistory().catch(() => {});
    };
    const openDetail = async (id: string) => {
        detailTarget.current = id;
        const request = ++detailRequest.current;
        const result = await getExpansion(id);
        if (request === detailRequest.current) setDetail(result);
    };
    const expansionID = params.get('expansion_id');
    useEffect(() => {
        if (expansionID) void openDetail(expansionID).catch(() => {});
    }, [expansionID]);
    useEffect(() => {
        if (!detail) return;
        const timer = window.setInterval(() => {
            if (detailTarget.current === detail.record.id) void openDetail(detail.record.id).catch(() => {});
        }, 5000);
        return () => window.clearInterval(timer);
    }, [detail?.record.id]);
    useEffect(() => () => { previewRequest.current += 1; detailRequest.current += 1; detailTarget.current = undefined; }, []);
    useEffect(() => {
        if (!importBatches.length) return;
        let active = true;
        const refresh = async () => {
            try {
                const statuses = await Promise.all(importBatches.map(async id => ({ id, status: await getPaperImportStatus(id) })));
                if (!active) return;
                const completed = statuses.filter(item => ['succeeded', 'completed_with_errors', 'failed', 'canceled'].includes(item.status)).map(item => item.id);
                if (completed.length) {
                    setImportBatches(current => current.filter(id => !completed.includes(id)));
                    importsChanged.current();
                }
            } catch {
                // The persisted import continues; retry its status on the next tick.
            }
        };
        void refresh();
        const timer = window.setInterval(() => void refresh(), 2500);
        return () => { active = false; window.clearInterval(timer); };
    }, [dataset, importBatches]);
    const enter = (kind: PaperTask) => {
        setMode(kind);
        setLaunch(undefined);
    };
    const goChat = (id: string, autoReview = false) => {
        window.dispatchEvent(new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT));
        navigate(getChatConversationPath(id) + (autoReview ? '?paper_auto_review=1' : ''));
    };
    const submit = async () => {
        if (!launch || busy) return;
        if (launch.direction && !instruction.trim()) {
            message.warning(t('paperTasks.enterTopic'));
            return;
        }
        setBusy(true);
        try {
            if (!launch.direction) {
                const selection = await resolvePaperSelection(dataset, selected);
                if (selection.invalid.length) {
                    Modal.error({ title: t('paperTasks.invalidSelection'), content: <List dataSource={selection.invalid} renderItem={item => <List.Item>{item.document_id}：{item.reason}</List.Item>} /> });
                    return;
                }
                if (!selection.papers.length) {
                    message.warning(t('paperTasks.selectConfirmedPaper'));
                    return;
                }
            }
            const body = {
                kind: launch.kind, dataset_id: dataset, document_ids: launch.direction ? [] : selected,
                direction: launch.direction ? instruction.trim() : undefined,
                user_instruction: launch.direction ? undefined : instruction.trim(), reflection_first: reflectionFirst,
            };
            const fingerprint = JSON.stringify(body);
            if (requestInput.current && requestInput.current !== fingerprint) requestKey.current = crypto.randomUUID();
            requestInput.current = fingerprint;
            const result = await launchPaperTask({ ...body, request_key: requestKey.current });
            setLaunch(undefined);
            setMode(undefined);
            goChat(result.conversation_id, launch.kind === 'review');
        } catch {
            // The request client displays the server error; keep the form for retry.
        } finally {
            setBusy(false);
        }
    };
    const contentReady = (source: ExpansionDetail, id: string) => source.contents.some(content => content.paper_id === id && content.status === 'ready' && !!content.content_hash);
    const contentStatus = (source: ExpansionDetail, id: string) => {
        const content = source.contents.find(item => item.paper_id === id);
        return t(`paperTasks.content.${content?.status === 'ready' && !content.content_hash ? 'pending' : content?.status || 'pending'}`);
    };
    const refreshImportPreview = async (source: ExpansionDetail) => {
        const request = ++previewRequest.current;
        setOperation(current => current ? { ...current, previewStatus: 'loading', error: undefined } : current);
        try {
            const items = await previewPaperImport(distinctPapers(source.record.result.papers || []).map(item => item.paper.paper_id), source.record.dataset_id);
            if (request === previewRequest.current) setOperation(current => current ? { ...current, preview: items, previewStatus: 'ready' } : current);
        } catch {
            if (request === previewRequest.current) setOperation(current => current ? { ...current, previewStatus: 'error' } : current);
        }
    };
    const openOperation = (kind: ResultOperation['kind']) => {
        if (!detail || busy || (kind === 'import' && !canImport)) return;
        previewRequest.current += 1;
        const papers = distinctPapers(detail.record.result.papers || []);
        setOperation({
            kind, source: detail,
            selected: kind === 'review' ? papers.filter(item => contentReady(detail, item.paper.paper_id)).map(item => item.paper.paper_id) : [],
            preview: [], previewStatus: kind === 'import' ? 'loading' : 'ready', requestKey: crypto.randomUUID(),
        });
        if (kind === 'import') void refreshImportPreview(detail);
    };
    const closeOperation = () => {
        if (busy) return;
        previewRequest.current += 1;
        setOperation(undefined);
    };
    const changeSelection = (keys: Key[]) => {
        const ids = keys.map(String);
        setOperation(current => current ? { ...current, selected: ids, requestKey: crypto.randomUUID(), error: undefined } : current);
    };
    const submitOperation = async () => {
        if (!operation || busy || operation.previewStatus !== 'ready') return;
        if (operation.selected.length < (operation.kind === 'review' ? 2 : 1)) return;
        if (operation.kind === 'import' && !canImport) return;
        setBusy(true);
        try {
            if (operation.kind === 'review') {
                const result = await launchPaperTask({
                    kind: 'review', dataset_id: operation.source.record.dataset_id,
                    expansion_id: operation.source.record.id, expansion_version: operation.source.record.version,
                    paper_ids: operation.selected, request_key: operation.requestKey,
                });
                setOperation(undefined);
                goChat(result.conversation_id);
            } else {
                const chosen = operation.preview.filter(item => operation.selected.includes(item.work_id) && item.disposition === 'downloadable');
                if (chosen.length !== operation.selected.length) throw new Error('Import preview changed');
                const result = await startReferenceImport(operation.source.record.dataset_id, [], chosen, operation.requestKey);
                setImportBatches(current => [...current, result.batch_id]);
                importsChanged.current();
                setOperation(undefined);
                message.success(t('paperTasks.importSubmitted'));
                void openDetail(operation.source.record.id).catch(() => {});
            }
        } catch {
            setOperation(current => current ? { ...current, error: t('paperTasks.submitFailed') } : current);
        } finally {
            setBusy(false);
        }
    };
    const closeDetail = () => {
        detailRequest.current += 1;
        detailTarget.current = undefined;
        setDetail(undefined);
        if (params.has('expansion_id')) {
            const next = new URLSearchParams(params);
            next.delete('expansion_id');
            setParams(next);
        }
    };
    const available = detail ? distinctPapers(detail.record.result.papers || []).filter(item => contentReady(detail, item.paper.paper_id)).length : 0;
    const operationPapers = operation ? distinctPapers(operation.source.record.result.papers || []) : [];
    const selectedSeeds = operationPapers.filter(item => item.is_seed && operation?.selected.includes(item.paper.paper_id)).length;
    const selectionReason = (item: WorkflowPaper) => {
        if (!operation) return '';
        if (operation.kind === 'review') return contentReady(operation.source, item.paper.paper_id) ? '' : contentStatus(operation.source, item.paper.paper_id);
        if (operation.previewStatus !== 'ready') return t('paperTasks.checkingImport');
        const preview = operation.preview.find(entry => entry.work_id === item.paper.paper_id);
        return preview?.disposition === 'downloadable' ? '' : t(`paperTasks.importStatus.${preview?.disposition || 'metadata_only'}`);
    };
    const sourceTag = (item: WorkflowPaper) => <Tag>{t(item.is_seed ? 'paperTasks.seed' : 'paperTasks.expanded')}</Tag>;
    return <>
        <Space wrap style={{ marginBottom: 12 }}>
            <Dropdown trigger={['click']} menu={{ items: [
                { key: 'papers', label: t('paperTasks.expandPapers'), onClick: () => enter('expansion') },
                { key: 'direction', label: t('paperTasks.expandDirection'), onClick: () => openLaunch('expansion', true) },
            ] }}><Button>{taskLabel('expansion')} <DownOutlined /></Button></Dropdown>
            <Dropdown trigger={['click']} menu={{ items: [
                { key: 'history', label: t('paperTasks.reviewHistory'), onClick: openHistory },
                { key: 'papers', label: t('paperTasks.reviewPapers'), onClick: () => enter('review') },
                { key: 'direction', label: t('paperTasks.reviewDirection'), onClick: () => openLaunch('review', true) },
            ] }}><Button>{taskLabel('review')} <DownOutlined /></Button></Dropdown>
            <Button onClick={() => enter('reflection')}>{taskLabel('reflection')}</Button>
            <Button onClick={() => enter('ppt')}>{taskLabel('ppt')}</Button>
            <Button onClick={openHistory}>{t('paperTasks.records')}</Button>
        </Space>
        {mode && <Alert style={{ marginBottom: 12 }} type="info" message={<Space wrap>
            <span>{t('paperTasks.choosePapers', { task: taskLabel(mode), count: selected.length })}</span>
            <Button type="primary" disabled={!selected.length} onClick={() => openLaunch(mode, false)}>{t('paperTasks.continue')}</Button>
            <Button onClick={() => setMode(undefined)}>{t('paperTasks.cancel')}</Button>
        </Space>} />}
        <Modal okText={t('paperTasks.start')} cancelText={t('paperTasks.cancel')} open={!!launch}
            title={launch ? <Space>{taskLabel(launch.kind)}{launch.kind !== 'expansion' && <Tag>{t('paperTasks.demo')}</Tag>}</Space> : ''}
            onCancel={() => !busy && setLaunch(undefined)} onOk={() => void submit()} confirmLoading={busy}>
            {launch?.kind === 'review' && <Alert type="info" showIcon message={t('paperTasks.reviewStartHint')} />}
            <Typography.Paragraph style={{ marginTop: 12, marginBottom: 4 }}>{t(launch?.direction ? 'paperTasks.topic' : 'paperTasks.additional')}</Typography.Paragraph>
            <Input.TextArea aria-label={t(launch?.direction ? 'paperTasks.topic' : 'paperTasks.additional')}
                placeholder={t(launch?.direction ? 'paperTasks.topicPlaceholder' : `paperTasks.placeholder.${launch?.kind || 'expansion'}`)}
                value={instruction} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setInstruction(event.target.value)} />
            {launch?.kind === 'ppt' && <Checkbox style={{ marginTop: 12 }} checked={reflectionFirst} onChange={(event: CheckboxChangeEvent) => setReflectionFirst(event.target.checked)}>{t('paperTasks.reflectionFirst')}</Checkbox>}
        </Modal>
        <Drawer open={history} title={t('paperTasks.personalRecords')} width={780} onClose={() => setHistory(false)}>
            <Button onClick={() => void refreshHistory()}>{t('paperTasks.refresh')}</Button>
            <List dataSource={records} locale={{ emptyText: t('paperTasks.noRecords') }} renderItem={record => <List.Item actions={[
                <Button key="view" onClick={() => void openDetail(record.id).catch(() => {})}>{t('paperTasks.viewResult')}</Button>,
            ]}><List.Item.Meta title={record.title} description={`${statusLabel(record.status)} · ${t('paperTasks.paperCount', { count: distinctPapers(record.result?.papers || []).length })}`} /></List.Item>} />
        </Drawer>
        <Drawer open={!!detail} title={t('paperTasks.resultTitle')} width={1000} onClose={closeDetail}>
            {detail && <>
                <Typography.Title level={4} ellipsis={{ rows: 2 }}>{detail.record.title}</Typography.Title>
                <Tag>{statusLabel(detail.record.status)}</Tag>
                {detail.chat_status !== 'available' && <Alert type="warning" message={t(detail.chat_status === 'trashed' ? 'paperTasks.expansionTrashed' : 'paperTasks.expansionDeleted')} />}
                <Space wrap style={{ margin: '12px 0' }}>
                    <Button disabled={detail.chat_status !== 'available'} onClick={() => goChat(detail.record.conversation_id)}>{t('paperTasks.returnExpansion')}</Button>
                    <Button type="primary" disabled={!detail.record.version} onClick={() => openOperation('review')}>{t('paperTasks.generateReview')}</Button>
                    <Button disabled={!canImport} onClick={() => openOperation('import')}>{t('paperTasks.import')}</Button>
                    <Button danger onClick={() => Modal.confirm({
                        title: t('paperTasks.deleteTitle'), content: t('paperTasks.deleteHint'), okText: t('paperTasks.confirmDelete'), cancelText: t('paperTasks.cancel'), okButtonProps: { danger: true },
                        onOk: async () => { await deleteExpansion(detail.record.id); closeDetail(); await refreshHistory(); },
                    })}>{t('paperTasks.deleteResult')}</Button>
                </Space>
                <Alert type={available >= 2 ? 'success' : 'info'} message={t('paperTasks.availableCount', { count: available })} />
                <Typography.Paragraph ellipsis={{ rows: 3, expandable: true, symbol: t('paperTasks.expand') }} style={{ whiteSpace: 'pre-wrap', marginTop: 12 }}>{detail.record.result.user_instruction}</Typography.Paragraph>
                <Typography.Paragraph>{t('paperTasks.directions', { value: detail.record.result.selected_directions?.join('；') || t('paperTasks.undetermined') })}</Typography.Paragraph>
                <Table<WorkflowPaper> rowKey={(item: WorkflowPaper) => item.paper.paper_id} dataSource={distinctPapers(detail.record.result.papers || [])} pagination={false} columns={[
                    { title: t('paperTasks.paper'), width: 280, render: (_: unknown, item: WorkflowPaper) => {
                        const title = item.paper.title || item.paper.paper_id;
                        if (item.is_seed) {
                            const document = detail.presence?.find(entry => entry.work_id === item.paper.paper_id)?.current_dataset.find(entry => entry.dataset_id && entry.document_id);
                            return document ? <Link to={`/lib/knowledge/knowledge/${encodeURIComponent(document.dataset_id)}/${encodeURIComponent(document.document_id)}`} target="_blank" rel="noopener noreferrer">{title}</Link> : title;
                        }
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
                    { title: t('paperTasks.source'), width: 80, render: (_: unknown, item: WorkflowPaper) => sourceTag(item) },
                    { title: t('paperTasks.fullText'), width: 140, render: (_: unknown, item: WorkflowPaper) => {
                        const content = detail.contents.find(entry => entry.paper_id === item.paper.paper_id);
                        return <Space direction="vertical"><span>{contentStatus(detail, item.paper.paper_id)}</span>
                            {content?.error && <Typography.Text type="danger">{content.error}</Typography.Text>}
                            {(content?.status === 'failed' || content?.status === 'unavailable') && <Button onClick={async () => { await retryPaperContent(item.paper.paper_id); await openDetail(detail.record.id); }}>{t('paperTasks.retryContent')}</Button>}
                        </Space>;
                    } },
                    { title: t('paperTasks.importState'), width: 100, render: (_: unknown, item: WorkflowPaper) => {
                        const presence = detail.presence?.find(entry => entry.work_id === item.paper.paper_id);
                        return t(`paperTasks.importStatus.${presence?.current_dataset.length ? 'already_present' : presence?.importing ? 'importing' : 'not_imported'}`);
                    } },
                    { title: <Space>{t('paperTasks.relations')}<Tag>{t('paperTasks.demo')}</Tag></Space>, render: (_: unknown, item: WorkflowPaper) => <Typography.Paragraph ellipsis={{ rows: 3, expandable: true, symbol: t('paperTasks.expand') }}>{[item.seed_relation, item.direction_relation, item.key_contribution].filter(Boolean).join('；')}</Typography.Paragraph> },
                ]} />
                <Typography.Title level={5}>{t('paperTasks.progress')}</Typography.Title>
                <List dataSource={Object.entries(detail.record.progress || {}).filter(([key, value]) => stages.includes(key) && value && typeof value === 'object').sort(([a], [b]) => stages.indexOf(a) - stages.indexOf(b))}
                    renderItem={([key, value]) => <List.Item><List.Item.Meta title={t(`paperTasks.stage.${key}`)} description={<><div>{value.candidate_directions?.join('；')}</div><div>{value.papers?.map(item => item.paper.title).filter(Boolean).join('；')}</div></>} /></List.Item>} />
                <Typography.Title level={5}>{t('paperTasks.rounds')}</Typography.Title>
                <List dataSource={detail.rounds} renderItem={round => <List.Item><details>
                    <summary>{t('paperTasks.round', { count: round.version })} · {statusLabel(round.status)} · {t('paperTasks.paperCount', { count: distinctPapers(round.result.papers || []).length })}</summary>
                    <Typography.Paragraph style={{ whiteSpace: 'pre-wrap' }}>{round.result.user_instruction}</Typography.Paragraph>
                    <Typography.Paragraph>{t('paperTasks.directions', { value: round.result.selected_directions?.join('；') })}</Typography.Paragraph>
                    <ul>{distinctPapers(round.result.papers || []).map(item => <li key={item.paper.paper_id}>{item.paper.title || item.paper.paper_id} {sourceTag(item)}</li>)}</ul>
                </details></List.Item>} />
                <Typography.Title level={5}>{t('paperTasks.generated')}</Typography.Title>
                <List dataSource={detail.handoffs} renderItem={item => <List.Item><Button onClick={() => goChat(item.conversation_id)}>{t('paperTasks.viewTask', { task: taskLabel(item.kind) })}</Button></List.Item>} />
            </>}
        </Drawer>
        <Modal open={!!operation} width={720} title={t(operation?.kind === 'review' ? 'paperTasks.reviewDialog' : 'paperTasks.import')}
            onCancel={closeOperation} onOk={() => void submitOperation()} cancelText={t('paperTasks.cancel')} confirmLoading={busy}
            okText={t(operation?.kind === 'review' ? 'paperTasks.confirmReview' : 'paperTasks.confirmImport', { count: operation?.selected.length || 0 })}
            okButtonProps={{ disabled: !operation || operation.previewStatus !== 'ready' || operation.selected.length < (operation.kind === 'review' ? 2 : 1) || (operation.kind === 'import' && !canImport) }}>
            {operation && <>
                <Alert type="info" message={t(operation.kind === 'review' ? 'paperTasks.reviewSelectionHint' : 'paperTasks.importSelectionHint')} />
                <Typography.Paragraph style={{ marginTop: 12 }} aria-live="polite">{t('paperTasks.selectionCount', { count: operation.selected.length, seeds: selectedSeeds, expanded: operation.selected.length - selectedSeeds })}</Typography.Paragraph>
                {operation.error && <Alert type="error" showIcon message={operation.error} style={{ marginBottom: 12 }} />}
                {operation.previewStatus === 'error' && <Alert type="error" message={t('paperTasks.previewFailed')} action={<Button onClick={() => void refreshImportPreview(operation.source)}>{t('paperTasks.retry')}</Button>} style={{ marginBottom: 12 }} />}
                <Table<WorkflowPaper> rowKey={(item: WorkflowPaper) => item.paper.paper_id} pagination={false} loading={operation.previewStatus === 'loading'} dataSource={operationPapers}
                    rowSelection={{ selectedRowKeys: operation.selected, onChange: changeSelection, getCheckboxProps: (item: WorkflowPaper) => ({ disabled: busy || !!selectionReason(item), 'aria-label': t('paperTasks.selectPaper', { title: item.paper.title || item.paper.paper_id }) }) }}
                    columns={[
                        { title: t('paperTasks.paper'), render: (_: unknown, item: WorkflowPaper) => item.paper.title || item.paper.paper_id },
                        { title: t('paperTasks.source'), width: 80, render: (_: unknown, item: WorkflowPaper) => sourceTag(item) },
                        { title: t(operation.kind === 'review' ? 'paperTasks.fullText' : 'paperTasks.importState'), width: 180, render: (_: unknown, item: WorkflowPaper) => selectionReason(item) || t(operation.kind === 'review' ? 'paperTasks.content.ready' : 'paperTasks.canImport') },
                    ]} />
            </>}
        </Modal>
    </>;
}
