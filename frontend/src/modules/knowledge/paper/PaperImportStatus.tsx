import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react';
import { Badge, Button, Empty, Popover, Tabs, Tooltip } from 'antd';
import { BellOutlined, CloseOutlined, DownOutlined, RightOutlined } from '@ant-design/icons';
import { getAcademicImportBatch, listAcademicImportBatches, type ImportBatchDetail, type ImportBatchPage, type ImportOperation, type ImportOperationItem } from '../api/academicReferences';
import { usePaperNotifications, type PaperNotification } from './usePaperNotifications';

function conciseError(error = '', provider?: string) {
  const source = provider === 'arxiv' ? 'arXiv' : '服务';
  if (/\b404\b/.test(error)) return `${source}接口暂不可用（404），请检查服务版本`;
  if (/\b429\b/.test(error)) return `${source}请求受限（429），请稍后重试`;
  if (/\b503\b/.test(error)) return `${source}暂不可用（503），请稍后重试`;
  if (/deadline exceeded|timed?\s*out|timeout/i.test(error)) return `${source}请求超时，请稍后重试`;
  return error.length > 140 ? `${error.slice(0, 140)}…` : error || '操作未完成，请查看详情';
}
const operationNames: Record<string, string> = { paper_expansion: '扩展导入', single_reference: '引用导入', paper_references: '参考文献导入', collection_references: '文献集合导入', conversation_sources: '对话来源导入', paper_link: '链接导入', pdf_upload: '上传 PDF', bibliography: '题录导入', zotero: 'Zotero 导入', library_backup: '备份恢复' };
const statusNames: Record<string, string> = { running: '处理中', failed: '失败', partial_failed: '部分失败', skipped: '已存在', metadata_only: '题录已保存', canceled: '已取消', succeeded: '已完成', removed: '已移出论文库', awaiting_association: '待关联' };
const itemNames: Record<string, string> = { importing: '导入中', processing: '处理中', ready: '已入库', metadata_only: '题录已保存', skipped: '已存在', failed: '导入失败', processing_failed: '处理失败', canceled: '已取消', deleted: '文档已删除', removed: '已移出论文库', awaiting_association: 'PDF 已上传，待关联' };
const enrichmentStages: Record<string, string> = { metadata: '补全元信息', pdf_lookup: '查找 PDF', acquire: '准备下载', download: '下载 PDF' };
const providers: Record<string, string> = { arxiv: 'arXiv', local: '本地 PDF', web: '网页', doi: 'DOI', zotero: 'Zotero' };
function resultSummary(summary: ImportOperation['summary']) {
  const results = [[summary.ready, '已入库'], [summary.metadata_only, '题录已保存'], [summary.skipped, '已存在'], [summary.active, '处理中'], [summary.failed, '失败'], [summary.canceled, '已取消'], [summary.deleted, '文档已删除'], [summary.removed, '已移出论文库']].filter(([count]) => Number(count) > 0);
  return results.length > 1 ? results.map(([count, label]) => `${label} ${count}`).join(' · ') : '';
}
function operationTime(operation: ImportOperation) {
  return new Date(operation.created_at).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
}
function operationTitle(operation: ImportOperation) {
  return operation.source.title || operation.source.file_name || (operation.entry_type === 'bibliography' ? '粘贴题录' : operationTime(operation));
}
function operationMetadata(operation: ImportOperation) {
  const hasSource = !!(operation.source.title || operation.source.file_name || operation.entry_type === 'bibliography');
  return [hasSource ? operationTime(operation) : '', operationNames[operation.entry_type], operation.source.format?.toUpperCase()].filter(Boolean).join(' · ');
}
interface Props {
  datasetId: string;
  page: ImportBatchPage;
  canWrite: boolean;
  onRetry: (batch: string) => Promise<void>;
  onOpenPaper: (id: string) => void;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onResumeAssociation?: (item: ImportOperationItem) => void;
  notifications?: PaperNotification[];
  syncError?: string | null;
  refreshing?: boolean;
  onRefresh?: () => void;
}
export default function PaperImportStatus({ datasetId, page, canWrite, onRetry, onOpenPaper, open, onOpenChange, onResumeAssociation, notifications = [], syncError, refreshing = false, onRefresh }: Props) {
  const panelId = useId();
  const [extraPages, setExtraPages] = useState<Array<{ cursor: string; data: ImportBatchPage }>>([]);
  const [expanded, setExpanded] = useState<string>();
  const [detail, setDetail] = useState<ImportBatchDetail>();
  const [loading, setLoading] = useState(false);
  const [retrying, setRetrying] = useState<string>();
  const [acting, setActing] = useState<string>();
  const actionBusy = useRef(false);
  const [tab, setTab] = useState('records');
  const wasOpen = useRef(false);
  const busy = useRef(false);
  const generation = useRef(0);
  const detailCursors = useRef<string[]>([]);
  const expandedRef = useRef(expanded);
  expandedRef.current = expanded;
  const extraPagesRef = useRef(extraPages);
  extraPagesRef.current = extraPages;
  const refreshRef = useRef<() => Promise<void>>(async () => {});
  const { notifications: requestErrors, notify, resolve } = usePaperNotifications(datasetId);
  useEffect(() => {
    const current = ++generation.current;
    return () => { if (generation.current === current) generation.current++; };
  }, [datasetId]);
  const run = async (action: () => Promise<void>, id: string, title: string, importBatchId?: string) => {
    if (busy.current) return;
    busy.current = true; setLoading(true);
    const current = generation.current;
    try { await action(); if (generation.current === current) resolve(id); }
    catch (cause) {
      if (generation.current === current) notify({ id, title, importBatchId, message: cause instanceof Error ? cause.message : '请求失败，请重试', action: { label: '重试', onClick: () => run(action, id, title, importBatchId) } });
    } finally { if (generation.current === current) { busy.current = false; setLoading(false); } }
  };
  useEffect(() => {
    let canceled = false;
    const refresh = async () => {
      const id = expandedRef.current;
      try {
        const pages = await Promise.all(extraPagesRef.current.map(async item => ({ cursor: item.cursor, data: await listAcademicImportBatches(datasetId, item.cursor) })));
        if (!canceled) { setExtraPages(pages); resolve('records-refresh'); }
      } catch (cause) {
        if (!canceled) notify({ id: 'records-refresh', title: '导入记录刷新失败', message: cause instanceof Error ? cause.message : '记录刷新失败，请重试', action: { label: '重新刷新', onClick: () => refreshRef.current() } });
      }
      if (!id || canceled || expandedRef.current !== id) return;
      try {
        const refreshed = await Promise.all([getAcademicImportBatch(id), ...detailCursors.current.map(cursor => getAcademicImportBatch(id, cursor))]);
        if (!canceled && expandedRef.current === id) {
          setDetail({ ...refreshed[0], items: [...new Map(refreshed.flatMap(part => part.items).map(item => [item.item_id, item])).values()], next_cursor: refreshed[refreshed.length - 1].next_cursor });
          resolve(`detail:${id}`);
        }
      } catch (cause) {
        if (!canceled) notify({ id: `detail:${id}`, importBatchId: id, title: '导入详情刷新失败', message: cause instanceof Error ? cause.message : '详情刷新失败，请重试', action: { label: '重新刷新', onClick: () => refreshRef.current() } });
      }
    };
    refreshRef.current = refresh;
    void refresh();
    return () => { canceled = true; };
  }, [datasetId, page, notify, resolve]);
  const operations = [...new Map([...page.items, ...extraPages.flatMap(item => item.data.items)].map(item => [item.id, item])).values()];
  const nextCursor = extraPages.length ? extraPages[extraPages.length - 1].data.next_cursor : page.next_cursor;
  const notices = [...new Map([...notifications, ...requestErrors, ...(syncError ? [{ id: 'imports-sync', title: '导入记录同步失败', message: syncError, action: onRefresh ? { label: '重新同步', onClick: onRefresh, disabled: refreshing } : undefined }] : [])].map(item => [item.id, item])).values()];
  const pendingBatches = new Set(operations.filter(item => item.summary.needs_action > 0).map(item => item.id));
  const standaloneNotices = notices.filter(item => !item.importBatchId || !pendingBatches.has(item.importBatchId));
  const pendingCount = page.notification.needs_action + standaloneNotices.length;
  useEffect(() => {
    if (open && !wasOpen.current) setTab(pendingCount > 0 ? 'pending' : 'records');
    wasOpen.current = open;
  }, [open, pendingCount]);
  const toggle = (id: string) => {
    if (expanded === id) { setExpanded(undefined); return; }
    setExpanded(id); expandedRef.current = id; detailCursors.current = []; setDetail(undefined);
    void run(async () => { const result = await getAcademicImportBatch(id); if (expandedRef.current === id) setDetail(result); }, `detail:${id}`, '导入详情加载失败', id);
  };
  const retry = async (id: string) => {
    if (retrying || busy.current) return;
    setRetrying(id);
    const current = generation.current;
    try {
      await onRetry(id);
      if (generation.current !== current) return;
      resolve(`retry:${id}`);
      if (expandedRef.current === id) await run(async () => { const result = await getAcademicImportBatch(id); if (expandedRef.current === id) setDetail(result); }, `detail:${id}`, '导入详情加载失败', id);
    } catch (cause) {
      if (generation.current === current) notify({ id: `retry:${id}`, importBatchId: id, title: '导入重试提交失败', message: cause instanceof Error ? cause.message : '重试提交失败，请稍后重试', action: { label: '重新提交', onClick: () => retry(id) } });
    } finally { if (generation.current === current) setRetrying(undefined); }
  };
  const actOnNotice = async (notice: PaperNotification) => {
    if (!notice.action || actionBusy.current) return;
    actionBusy.current = true;
    setActing(notice.id);
    if (notice.action.closeOnClick) onOpenChange(false);
    try { await notice.action.onClick(); }
    catch (cause) { notify({ ...notice, message: cause instanceof Error ? cause.message : '操作未完成，请重试' }); }
    finally { actionBusy.current = false; setActing(undefined); }
  };
  const renderNotice = (notice: PaperNotification) => <article className="paper-library-notice" key={notice.id}>
    <strong>{notice.title}</strong>
    <details className="paper-library-import-error"><summary>{conciseError(notice.message)}</summary><p>{notice.message}</p></details>
    {notice.action && <Button type="link" size="small" loading={acting === notice.id} disabled={loading || !!acting || !!retrying || notice.action.disabled} onClick={() => void actOnNotice(notice)}>{notice.action.label}</Button>}
  </article>;
  const renderPaper = (item: ImportOperationItem) => {
    const message = item.current_state === 'processing_failed' ? item.current_document?.processing_error || item.error_message : item.error_message;
    const failed = ['failed', 'processing_failed'].includes(item.current_state);
    const content = <Tooltip title={item.title}><span className="paper-library-import-paper-title">{item.title}</span></Tooltip>;
    return <li key={item.item_id} className={`paper-library-import-paper${item.resolved_later ? ' is-resolved' : ''}`}>
      {item.current_document && !item.document_deleted
        ? <a className="paper-library-import-title-link" href={`/lib/knowledge/knowledge/${encodeURIComponent(item.target_dataset_id)}/${encodeURIComponent(item.current_document.document_id)}`}>{content}</a>
        : item.library_paper_id && !item.document_deleted
          ? <button type="button" className="paper-library-import-title-link" onClick={() => { onOpenChange(false); onOpenPaper(item.library_paper_id!); }}>{content}</button>
          : content}
      <div className="paper-library-import-paper-meta"><span>{[providers[item.source_provider || ''] || item.source_provider, item.source_version].filter(Boolean).join(' · ')}</span>
        <Badge className="paper-library-import-status" status={failed && !item.resolved_later ? 'error' : ['importing', 'processing'].includes(item.current_state) ? 'processing' : ['ready', 'metadata_only'].includes(item.current_state) ? 'success' : 'default'} text={`${item.result_kind === 'enrichment' && item.current_state === 'importing' ? `题录已保存 · ${enrichmentStages[item.stage] || '后台补全中'}` : item.result_kind === 'enrichment' && failed ? '题录已保存 · 补全失败' : itemNames[item.current_state] || item.current_state}${failed && item.resolved_later ? '（已解决）' : ''}`} />
      </div>
      {item.current_state === 'awaiting_association' && canWrite && onResumeAssociation && <Button size="small" type="link" onClick={() => { onOpenChange(false); onResumeAssociation(item); }}>继续关联</Button>}
      {item.current_state !== 'removed' && (item.resolved_later ? <p className="paper-library-import-note">已通过后续操作入库</p>
        : item.status === 'skipped' ? <p className="paper-library-import-note">{item.current_document ? '本次跳过，复用已有 PDF' : '本次跳过，题录已存在'}</p>
          : item.current_state === 'processing' ? <p className="paper-library-import-note">PDF 已保存 · 正文处理中</p> : null)}
      {failed && message && <details className="paper-library-import-error"><summary>{conciseError(message, item.current_state === 'processing_failed' ? undefined : item.source_provider)}</summary><p>{message}</p></details>}
      {!failed && item.result_kind === 'enrichment' && message && <details className="paper-library-import-note"><summary>已保存 PDF，部分来源查询有提示</summary><p>{message}</p></details>}
    </li>;
  };
  const renderOperation = (operation: ImportOperation) => {
    const selected = expanded === operation.id;
    const shown = selected && detail?.operation.id === operation.id ? detail.operation : operation;
    const resolved = shown.summary.failed > 0 && shown.summary.resolved_later === shown.summary.failed && !shown.summary.needs_action && !shown.summary.active;
    return <article className="paper-library-import-operation" key={operation.id}>
      <div className="paper-library-import-operation-head">
        <button type="button" className="paper-library-import-operation-toggle" disabled={loading} aria-expanded={selected} aria-controls={selected ? `${panelId}-${shown.id}` : undefined}
          aria-label={`${selected ? '收起' : '展开'}导入记录：${operationTitle(shown)}，${shown.summary.total} 篇`} onClick={() => toggle(operation.id)}>
          {selected ? <DownOutlined aria-hidden /> : <RightOutlined aria-hidden />}<span title={operationTitle(shown)}>{operationNames[shown.entry_type] || shown.entry_type}{shown.source.format ? ` · ${shown.source.format.toUpperCase()}` : ''} · {shown.summary.total} 篇</span>
        </button>
        <Badge className="paper-library-import-status" status={shown.summary.needs_action ? 'error' : shown.summary.active ? 'processing' : ['succeeded', 'metadata_only'].includes(shown.status) ? 'success' : 'default'} text={`${shown.status === 'succeeded' && shown.summary.active ? '已入库 · 后台解析中' : statusNames[shown.status] || shown.status}${resolved ? '（已解决）' : ''}`} />
      </div>
      {operationMetadata(shown) && <p className="paper-library-import-operation-source">{operationMetadata(shown)}</p>}
      {resultSummary(shown.summary) && <p className="paper-library-import-operation-summary">{resultSummary(shown.summary)}</p>}
      {shown.summary.resolved_later > 0 && <p className="paper-library-import-note">{shown.summary.resolved_later} 篇已通过后续操作入库</p>}
      {notices.filter(item => item.importBatchId === shown.id).map(renderNotice)}
      {canWrite && shown.summary.retryable > 0 && <div className="paper-library-import-operation-actions"><Button type="link" size="small" loading={retrying === shown.id} disabled={!!retrying || shown.summary.active > 0} onClick={() => void retry(shown.id)}>重试失败项（{shown.summary.retryable}）</Button></div>}
      {selected && <div id={`${panelId}-${shown.id}`} className="paper-library-import-operation-detail">{!detail || detail.operation.id !== shown.id ? <p role="status">{loading ? '正在加载本次论文…' : '详情加载未完成，请重试'}</p> : <>
        <ul>{detail.items.map(renderPaper)}</ul>
        {detail.next_cursor && <Button block type="text" aria-label="加载更多论文" aria-busy={loading} loading={loading} onClick={() => void run(async () => {
          const cursor = detail.next_cursor;
          const result = await getAcademicImportBatch(shown.id, cursor);
          if (expandedRef.current === shown.id) { if (!detailCursors.current.includes(cursor)) detailCursors.current.push(cursor); setDetail(previous => previous ? { ...result, items: [...new Map([...previous.items, ...result.items].map(item => [item.item_id, item])).values()] } : result); }
        }, `papers:${shown.id}`, '更多论文加载失败', shown.id)}>加载更多论文</Button>}
      </>}</div>}
    </article>;
  };
  const more = nextCursor && <Button block type="text" aria-label="加载更多记录" aria-busy={loading} loading={loading} onClick={() => void run(async () => {
    const result = await listAcademicImportBatches(datasetId, nextCursor);
    setExtraPages(previous => [...previous.filter(item => item.cursor !== nextCursor), { cursor: nextCursor, data: result }]);
  }, 'records-more', '更多导入记录加载失败')}>加载更多记录</Button>;
  const summary = `${syncError ? '导入进度暂不可用' : `${page.notification.active} 项进行中`} · ${pendingCount} 条待处理`;
  const activity = [syncError ? '导入进度暂不可用' : page.notification.active > 0 ? `${page.notification.active} 项进行中` : '', pendingCount > 0 ? `${pendingCount} 条待处理` : ''].filter(Boolean).join(' · ');
  return <Popover trigger="click" placement="bottomRight" arrow={false} destroyOnHidden open={open} onOpenChange={onOpenChange}
    overlayClassName="paper-library-tool-popover paper-library-import-popover"
    content={<section id={panelId} role="dialog" aria-label="通知" onKeyDown={event => { if (event.key === 'Escape') onOpenChange(false); }}>
      <div className="paper-library-import-notice-head"><div><strong>通知</strong>{activity && <p role="status">{activity}</p>}</div>
        <Button type="text" size="small" icon={<CloseOutlined />} aria-label="关闭通知" onClick={() => onOpenChange(false)} />
      </div>
      <Tabs activeKey={tab} onChange={setTab} destroyOnHidden items={[
        { key: 'pending', label: `待处理${pendingCount ? ` ${pendingCount}` : ''}`, children: <div className="paper-library-import-notice-body">
          {standaloneNotices.map(renderNotice)}
          {operations.filter(item => pendingBatches.has(item.id)).map(renderOperation)}
          {!pendingCount && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有待处理事项" />}
          {page.notification.needs_action > pendingBatches.size && <p className="paper-library-import-note">还有待处理的导入记录，请继续加载。</p>}
          {more}
        </div> },
        { key: 'records', label: '导入记录', children: <div className="paper-library-import-notice-body">
          {requestErrors.filter(item => !item.importBatchId).map(renderNotice)}
          {syncError && <p className="paper-library-import-note">{operations.length ? '以下为上次同步的记录，最新进度暂不可用。' : '导入记录暂时无法加载，请到待处理重试。'}</p>}
          {!operations.length && !syncError && (refreshing ? <p role="status">正在加载导入记录…</p> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无论文导入记录" />)}
          {operations.map(renderOperation)}{more}
        </div> },
      ]} />
    </section>}>
    <Button type="text" className={`paper-library-tool paper-library-notification ${open ? 'is-active' : ''}`} aria-label={`通知：${summary}`} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? panelId : undefined} title={`通知：${summary}`}
      onKeyDown={(event: KeyboardEvent<HTMLButtonElement>) => { if (event.key === 'Escape') onOpenChange(false); }}>
      <Badge count={pendingCount} dot={!pendingCount && page.notification.active > 0} color={pendingCount ? undefined : '#2858b8'} offset={[3, -2]}><BellOutlined /></Badge>
    </Button>
  </Popover>;
}
