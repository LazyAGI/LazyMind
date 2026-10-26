import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react';
import { Alert, Badge, Button, Empty, Popover, Tooltip } from 'antd';
import { BellOutlined, CloseOutlined, DownOutlined, RightOutlined } from '@ant-design/icons';
import { getAcademicImportBatch, listAcademicImportBatches, type ImportBatchDetail, type ImportBatchPage, type ImportOperation, type ImportOperationItem } from '../api/academicReferences';

function conciseError(error = '') {
  if (/\b429\b/.test(error)) return 'arXiv 请求受限（429），请稍后重试';
  if (/\b503\b/.test(error)) return 'arXiv 服务暂不可用（503），请稍后重试';
  if (/deadline exceeded|timed?\s*out|timeout/i.test(error)) return 'arXiv 请求超时，请稍后重试';
  return error.length > 140 ? `${error.slice(0, 140)}…` : error || '导入未完成，请查看详情';
}
const operationNames: Record<string, string> = { paper_expansion: '扩展导入', paper_link: '链接导入', pdf_upload: '上传 PDF', bibliography: '题录导入' };
const statusNames: Record<string, string> = { running: '处理中', failed: '失败', partial_failed: '部分失败', skipped: '已存在', metadata_only: '题录已保存', canceled: '已取消', succeeded: '已完成' };
const itemNames: Record<string, string> = { importing: '导入中', processing: '处理中', ready: '已入库', metadata_only: '题录已保存', skipped: '已存在', failed: '导入失败', processing_failed: '处理失败', canceled: '已取消', deleted: '文档已删除' };
const providers: Record<string, string> = { arxiv: 'arXiv', local: '本地 PDF', web: '网页', doi: 'DOI' };
function resultSummary(summary: ImportOperation['summary']) {
  const results = [[summary.ready, '已入库'], [summary.metadata_only, '题录已保存'], [summary.skipped, '已存在'], [summary.active, '处理中'], [summary.failed, '失败'], [summary.canceled, '已取消'], [summary.deleted, '文档已删除']].filter(([count]) => Number(count) > 0);
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
}

export default function PaperImportStatus({ datasetId, page, canWrite, onRetry, onOpenPaper, open, onOpenChange }: Props) {
  const panelId = useId();
  const [extraPages, setExtraPages] = useState<Array<{ cursor: string; data: ImportBatchPage }>>([]);
  const [expanded, setExpanded] = useState<string>();
  const [detail, setDetail] = useState<ImportBatchDetail>();
  const [loading, setLoading] = useState(false);
  const [retrying, setRetrying] = useState<string>();
  const [error, setError] = useState('');
  const busy = useRef(false);
  const generation = useRef(0);
  const detailCursors = useRef<string[]>([]);
  const expandedRef = useRef(expanded);
  expandedRef.current = expanded;
  const extraPagesRef = useRef(extraPages);
  extraPagesRef.current = extraPages;
  useEffect(() => {
    const current = ++generation.current;
    return () => { if (generation.current === current) generation.current++; };
  }, [datasetId]);
  useEffect(() => {
    let canceled = false;
    const refresh = async () => {
      const id = expandedRef.current;
      try {
        const pages = await Promise.all(extraPagesRef.current.map(async item => ({ cursor: item.cursor, data: await listAcademicImportBatches(datasetId, item.cursor) })));
        if (!canceled) setExtraPages(pages);
        if (id && !canceled && expandedRef.current === id) {
          const refreshed = await Promise.all([getAcademicImportBatch(id), ...detailCursors.current.map(cursor => getAcademicImportBatch(id, cursor))]);
          if (!canceled && expandedRef.current === id) setDetail({ ...refreshed[0], items: [...new Map(refreshed.flatMap(part => part.items).map(item => [item.item_id, item])).values()], next_cursor: refreshed[refreshed.length - 1].next_cursor });
        }
      } catch (cause) { if (!canceled) setError(cause instanceof Error ? cause.message : '导入记录刷新失败，请重试'); }
    };
    void refresh();
    return () => { canceled = true; };
  }, [datasetId, page]);
  const operations = [...new Map([...page.items, ...extraPages.flatMap(item => item.data.items)].map(item => [item.id, item])).values()];
  const nextCursor = extraPages.length ? extraPages[extraPages.length - 1].data.next_cursor : page.next_cursor;
  const run = async (action: () => Promise<void>) => {
    if (busy.current) return;
    busy.current = true; setLoading(true); setError('');
    const current = generation.current;
    try { await action(); }
    catch (cause) { if (generation.current === current) setError(cause instanceof Error ? cause.message : '导入记录加载失败，请重试'); }
    finally { if (generation.current === current) { busy.current = false; setLoading(false); } }
  };
  const toggle = (id: string) => {
    if (expanded === id) { setExpanded(undefined); return; }
    setExpanded(id); expandedRef.current = id; detailCursors.current = []; setDetail(undefined);
    void run(async () => { const result = await getAcademicImportBatch(id); if (expandedRef.current === id) setDetail(result); });
  };
  const retry = async (id: string) => {
    if (retrying || busy.current) return;
    setRetrying(id); setError('');
    try { await onRetry(id); if (expandedRef.current === id) setDetail(await getAcademicImportBatch(id)); }
    catch (cause) { setError(cause instanceof Error ? cause.message : '重试提交失败，请稍后重试'); }
    finally { setRetrying(undefined); }
  };
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
        <Badge className="paper-library-import-status" status={failed && !item.resolved_later ? 'error' : ['importing', 'processing'].includes(item.current_state) ? 'processing' : ['ready', 'metadata_only'].includes(item.current_state) ? 'success' : 'default'} text={`${itemNames[item.current_state] || item.current_state}${failed && item.resolved_later ? '（已解决）' : ''}`} />
      </div>
      {item.resolved_later ? <p className="paper-library-import-note">已通过后续操作入库</p>
        : item.status === 'skipped' ? <p className="paper-library-import-note">{item.current_document ? '本次跳过，复用已有 PDF' : '本次跳过，题录已存在'}</p>
          : item.current_state === 'processing' ? <p className="paper-library-import-note">PDF 已保存 · 正文处理中</p> : null}
      {failed && message && <details className="paper-library-import-error"><summary>{conciseError(message)}</summary><p>{message}</p></details>}
    </li>;
  };
  const summary = `${page.notification.active} 项进行中 · ${page.notification.needs_action} 项待处理`;
  const activity = [page.notification.active > 0 ? `${page.notification.active} 项进行中` : '', page.notification.needs_action > 0 ? `${page.notification.needs_action} 项待处理` : ''].filter(Boolean).join(' · ');
  return <Popover trigger="click" placement="bottomRight" arrow={false} destroyOnHidden open={open} onOpenChange={onOpenChange}
    overlayClassName="paper-library-tool-popover paper-library-import-popover"
    content={<section id={panelId} role="dialog" aria-label="论文导入通知" onKeyDown={event => { if (event.key === 'Escape') onOpenChange(false); }}>
      <div className="paper-library-import-notice-head"><div><strong>导入记录</strong>{activity && <p role="status">{activity}</p>}</div>
        <Button type="text" size="small" icon={<CloseOutlined />} aria-label="关闭导入通知" onClick={() => onOpenChange(false)} />
      </div>
      <div className="paper-library-import-notice-body">
        {error && <Alert type="error" showIcon message={error} />}
        {!operations.length && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无论文导入记录" />}
        {operations.map(operation => {
          const selected = expanded === operation.id;
          const shown = selected && detail?.operation.id === operation.id ? detail.operation : operation;
          const resolved = shown.summary.failed > 0 && shown.summary.resolved_later === shown.summary.failed && !shown.summary.needs_action && !shown.summary.active;
          return <article className="paper-library-import-operation" key={operation.id}>
            <div className="paper-library-import-operation-head">
              <button type="button" className="paper-library-import-operation-toggle" disabled={loading} aria-expanded={selected} aria-controls={selected ? `${panelId}-${shown.id}` : undefined}
                aria-label={`${selected ? '收起' : '展开'}导入记录：${operationTitle(shown)}，${shown.summary.total} 篇`} onClick={() => toggle(operation.id)}>
                {selected ? <DownOutlined aria-hidden /> : <RightOutlined aria-hidden />}
                <span title={operationTitle(shown)}>{operationTitle(shown)} · {shown.summary.total} 篇</span>
              </button>
              <Badge className="paper-library-import-status" status={shown.summary.needs_action ? 'error' : shown.summary.active ? 'processing' : ['succeeded', 'metadata_only'].includes(shown.status) ? 'success' : 'default'} text={`${statusNames[shown.status] || shown.status}${resolved ? '（已解决）' : ''}`} />
            </div>
            {operationMetadata(shown) && <p className="paper-library-import-operation-source">{operationMetadata(shown)}</p>}
            {resultSummary(shown.summary) && <p className="paper-library-import-operation-summary">{resultSummary(shown.summary)}</p>}
            {shown.summary.resolved_later > 0 && <p className="paper-library-import-note">{shown.summary.resolved_later} 篇已通过后续操作入库</p>}
            {canWrite && shown.summary.retryable > 0 && <div className="paper-library-import-operation-actions"><Button type="link" size="small" loading={retrying === shown.id} disabled={!!retrying || shown.summary.active > 0} onClick={() => void retry(shown.id)}>重试失败项（{shown.summary.retryable}）</Button></div>}
            {selected && <div id={`${panelId}-${shown.id}`} className="paper-library-import-operation-detail">{!detail ? <p role="status">{loading ? '正在加载本次论文…' : '加载未完成，请收起后重试'}</p> : <>
              <ul>{detail.items.map(renderPaper)}</ul>
              {detail.next_cursor && <Button block type="text" aria-label="加载更多论文" aria-busy={loading} loading={loading} onClick={() => void run(async () => { const result = await getAcademicImportBatch(shown.id, detail.next_cursor); if (expandedRef.current === shown.id) { detailCursors.current.push(detail.next_cursor); setDetail(previous => previous ? { ...result, items: [...previous.items, ...result.items] } : result); } })}>加载更多论文</Button>}
            </>}</div>}
          </article>;
        })}
        {nextCursor && <Button block type="text" aria-label="加载更多记录" aria-busy={loading} loading={loading} onClick={() => void run(async () => { const result = await listAcademicImportBatches(datasetId, nextCursor); setExtraPages(previous => [...previous, { cursor: nextCursor, data: result }]); })}>加载更多记录</Button>}
      </div>
    </section>}>
    <Button type="text" className={`paper-library-tool paper-library-notification ${open ? 'is-active' : ''}`} aria-label={`论文导入通知：${summary}`} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? panelId : undefined} title={`论文导入：${summary}`}
      onKeyDown={(event: KeyboardEvent<HTMLButtonElement>) => { if (event.key === 'Escape') onOpenChange(false); }}>
      <Badge count={page.notification.needs_action} dot={!page.notification.needs_action && page.notification.active > 0} color={page.notification.needs_action ? undefined : '#2858b8'} offset={[3, -2]}><BellOutlined /></Badge>
    </Button>
  </Popover>;
}
