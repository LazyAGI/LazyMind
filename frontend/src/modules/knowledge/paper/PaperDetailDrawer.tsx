import { useMemo, useRef, useState, type ChangeEvent } from 'react';
import { Alert, Button, Dropdown, Input, Modal, Tabs, Tag, Tooltip } from 'antd';
import { CloseOutlined, DownOutlined, ExclamationCircleOutlined, InfoCircleOutlined, ReadOutlined } from '@ant-design/icons';
import type { PaperLibraryDataV3, PaperRecord, Attachment } from './libraryModel';
import { createNote, readingSummary } from './libraryModel';
import { importMethodLabel, safePaperUrl, type PaperEdits } from './model';
import type { DocumentAcademic } from '../api/paperWorkflows';
import PaperMetadataForm from '../components/PaperTasks/PaperMetadataForm';
import { isStoredPdfAttachment } from './paperFilters';
import './PaperDetailDrawer.scss';

export type PaperDetailTab = 'overview' | 'attachments' | 'notes' | 'annotations' | 'relations';
interface Props {
  paper: PaperRecord;
  data: PaperLibraryDataV3;
  initialTab?: PaperDetailTab;
  initialEditing?: boolean;
  academic?: DocumentAcademic;
  onClose: () => void;
  onChange: (data: PaperLibraryDataV3) => Promise<void>;
  disabled?: boolean;
  onOpenAttachment?: (documentId: string) => void;
  onEdit?: () => void;
  onSave?: (edits: PaperEdits) => Promise<boolean>;
  editError?: string | null;
  onRetryIdentification?: () => Promise<void>;
  onReparse?: () => void;
  reparseDisabledReason?: string;
  onAddPdf?: () => void;
  onDetachAttachment?: (attachment: Attachment) => Promise<void>;
}

function sourceName(attachment?: Attachment) {
  if (!attachment) return '未记录';
  if (attachment.sourceProvider === 'arxiv' || attachment.source === 'arxiv') return 'arXiv';
  if (attachment.importMethod === 'upload' || attachment.sourceProvider === 'upload') return '上传文件';
  const url = safePaperUrl(attachment.sourceUrl);
  if (url) return new URL(url).hostname;
  return attachment.sourceProvider && attachment.sourceProvider !== 'manual' ? attachment.sourceProvider : '未记录';
}

export default function PaperDetailDrawer({ paper, data, initialTab = 'overview', initialEditing = false, academic, onClose, onChange, onOpenAttachment, onEdit, onSave, editError, onRetryIdentification, onReparse, reparseDisabledReason, onAddPdf, onDetachAttachment, disabled = false }: Props) {
  const [tab, setTab] = useState(initialTab);
  const [editing, setEditing] = useState(initialEditing);
  const [note, setNote] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const savingRef = useRef(false);
  const current = data.papers.find((item) => item.id === paper.id) || paper;
  const notes = useMemo(() => data.notes.filter((item) => current.noteIds.includes(item.id)), [data.notes, current.noteIds]);
  const attachments = data.attachments.filter((item) => current.attachmentIds.includes(item.id) && item.available !== false);
  const pdfs = attachments.filter(isStoredPdfAttachment);
  const summary = { ...readingSummary(data, current.id), attachments: attachments.length };
  const primary = attachments.find(item => item.isPrimary) || attachments[0];
  const method = primary?.importMethod || current.importMethod;
  const sourceUrl = safePaperUrl(primary?.sourceUrl);
  const identifyStatus = academic?.state.manual ? '已手动确认' : ({ pending: '待识别', processing: '识别中', succeeded: '识别成功', failed: '识别失败' }[academic?.state.status || ''] || '未记录');
  const readButton = <Button
    type="primary"
    icon={<ReadOutlined />}
    disabled={!pdfs.length || !onOpenAttachment}
    onClick={pdfs.length === 1 ? () => onOpenAttachment?.(pdfs[0].contentRef!) : undefined}
  >阅读{pdfs.length > 1 && <DownOutlined />}</Button>;

  const run = async (action: () => Promise<void>, fallback: string) => {
    if (disabled || savingRef.current) return;
    savingRef.current = true;
    setSaving(true);
    setError(null);
    try { await action(); }
    catch (failure) { setError(failure instanceof Error ? failure.message : fallback); }
    finally { savingRef.current = false; setSaving(false); }
  };
  const addNote = () => {
    if (!note.trim()) return;
    const next = createNote(current.id, note.trim());
    return run(async () => {
      await onChange({ ...data, notes: [...data.notes, next], papers: data.papers.map((item) => item.id === current.id ? { ...item, noteIds: [...item.noteIds, next.id] } : item) });
      setNote('');
    }, '笔记保存失败，请重试');
  };
  return <div className="paper-detail-mask" onClick={(event) => { if (event.target === event.currentTarget && !saving) onClose(); }}>
    <aside className="paper-detail-drawer" role="dialog" aria-modal="true" aria-label="文献详情">
      <header className="paper-detail-head">
        <div className="paper-detail-toolbar">
          <span className="paper-detail-kicker">文献档案</span>
          <div className="paper-detail-head-actions">
            {pdfs.length > 1 ? <Dropdown placement="bottomRight" trigger={['click']} menu={{ items: pdfs.map(attachment => ({
              key: attachment.id,
              label: `${attachment.name} · ${attachment.sourceVersion || '版本未知'}${attachment.isPrimary ? ' · 主附件' : ''}`,
              onClick: () => onOpenAttachment?.(attachment.contentRef!),
            })) }}>{readButton}</Dropdown> : <Tooltip title={!pdfs.length ? '请先在附件中添加 PDF' : undefined}>{readButton}</Tooltip>}
            <Button type="text" aria-label="关闭" icon={<CloseOutlined />} disabled={saving || (editing && disabled)} onClick={onClose} />
          </div>
        </div>
        <h2>{current.title}</h2>
        <p>{current.authors} · {current.venue} {current.year || ''}</p>
        <div className="paper-detail-facts"><span>{current.status === 'read' ? '已读' : current.status === 'reading' ? '阅读中' : '未读'}</span></div>
      </header>
      {!pdfs.length && <p className="paper-detail-read-hint"><InfoCircleOutlined /><span>请先在<button type="button" className="paper-detail-tab-link" disabled={saving || editing} onClick={() => setTab('attachments')}>附件</button>中添加 PDF</span></p>}
      <div className="paper-detail-summary">
        <div className="paper-detail-attachment-summary">
          <span><b>{summary.attachments}</b> 个附件</span>
          {current.parse === 'failed' && <>
            <span className="paper-detail-status-failed" role="status" aria-label="正文解析状态"><ExclamationCircleOutlined />正文解析失败</span>
            {onReparse && <Tooltip title={reparseDisabledReason}><Button size="small" disabled={disabled || saving || editing || !!reparseDisabledReason} onClick={onReparse}>重新解析</Button></Tooltip>}
          </>}
          {current.parse === 'parsing' && <span role="status" aria-label="正文解析状态">正文解析中</span>}
        </div>
        <span className="paper-detail-count"><b>{summary.notes}</b> 条笔记</span>
        <span className="paper-detail-count"><b>{summary.annotations}</b> 条批注</span>
      </div>
      {error && <Alert className="paper-detail-error" type="error" showIcon message="操作未完成" description={error} />}
      {current.associationConflict && <Alert type="warning" showIcon message="附件归属待核对" description={current.associationConflict} />}
      <Tabs activeKey={tab} onChange={(next: string) => { if (!saving && !editing) setTab(next as PaperDetailTab); }} items={[
        { key: 'overview', label: '概览', children: <div className="paper-detail-section">
          <div className="paper-detail-section-head">
            <h3>论文信息</h3>
            <div className="paper-detail-identification">
              {academic && <span className={academic.state.status === 'failed' && !academic.state.manual ? 'paper-detail-status-failed' : undefined} role="status" aria-label="论文信息自动识别">{identifyStatus}</span>}
              {academic?.state.status === 'failed' && onRetryIdentification && <Button size="small" aria-label="重试自动识别" disabled={disabled || saving || editing} loading={saving} onClick={() => void run(onRetryIdentification, '自动识别重试失败，请稍后重试')}>重试自动识别</Button>}
              {onSave && !editing && <Button size="small" disabled={disabled || saving} onClick={() => { onEdit?.(); setEditing(true); }}>编辑</Button>}
            </div>
          </div>
          {editing ? <>
            {editError && <Alert type="error" showIcon message="题录尚未保存" description={editError} />}
            <PaperMetadataForm value={{ ...current, abstract: current.abstract ?? current.metadata.abstract, arxiv_id: current.arxiv, tags: current.tags.join(';') }} disabled={disabled || saving} includeTags onSave={value => run(async () => {
              const saved = await onSave!({ title: value.title, authors: value.authors || '', venue: value.venue || '', year: value.year || 0, doi: value.doi, arxiv: value.arxiv_id, abstract: value.abstract, tags: (value.tags || '').split(/[;；]/) });
              if (saved) setEditing(false);
            }, '论文信息保存失败，请重试')} />
            <Button disabled={disabled || saving} onClick={() => setEditing(false)}>取消编辑</Button>
          </> : <>
            <dl><dt>标题</dt><dd>{current.title}</dd><dt>作者</dt><dd>{current.authors || '未填写'}</dd><dt>年份</dt><dd>{current.year || '未填写'}</dd><dt>会议／期刊</dt><dd>{current.venue || '未填写'}</dd><dt>DOI</dt><dd>{current.doi || '未填写'}</dd><dt>arXiv</dt><dd>{current.arxiv ? `${current.arxiv}${current.arxivVersion || ''}` : '未填写'}</dd><dt>摘要</dt><dd>{current.abstract ?? current.metadata.abstract ?? '未填写'}</dd></dl>
            <div className="paper-detail-tags">{current.tags.filter(tag => tag !== 'paper' && !tag.startsWith('arxiv:')).map(tag => <Tag key={tag}>{tag}</Tag>)}</div>
          </>}
          <div className="paper-detail-section-head"><h3>全文附件</h3></div>
          <p className="paper-detail-muted">{pdfs.length ? <button type="button" className="paper-detail-tab-link" onClick={() => setTab('attachments')}>PDF · {pdfs.length} 份，查看附件与版本</button> : attachments.length ? '尚未添加入库 PDF，可在附件页查看其他材料。' : '仅题录，尚未添加 PDF。'}</p>
          <div className="paper-detail-section-head"><h3>来源</h3></div>
          <dl><dt>导入方式</dt><dd>{method ? importMethodLabel[method] || '未记录' : '未记录'}</dd><dt>原文来源</dt><dd>{sourceName(primary)}</dd><dt>原始链接</dt><dd>{sourceUrl ? <a href={sourceUrl} target="_blank" rel="noopener noreferrer">{sourceUrl}</a> : '未记录'}</dd><dt>来源版本</dt><dd>{primary?.sourceVersion || '未记录'}</dd></dl>
        </div> },
        { key: 'attachments', label: `附件 ${summary.attachments}`, children: <div className="paper-detail-section"><div className="paper-detail-section-head"><h3>附件与版本</h3>{onAddPdf && <Button size="small" disabled={disabled || saving || editing} onClick={onAddPdf}>添加 PDF</Button>}</div>{attachments.length ? attachments.map(item => {
          const url = safePaperUrl(item.sourceUrl);
          return <div className="paper-detail-item" key={item.id}><strong>{item.name}</strong><span>{isStoredPdfAttachment(item) ? 'PDF' : item.kind === 'pdf' ? '原文链接' : '补充材料'} · {item.sourceVersion || '版本未知'}{item.isPrimary ? ' · 主附件' : ''}{item.size ? ` · ${(item.size / 1024 / 1024).toFixed(1)} MB` : ''}</span><span>导入方式：{item.importMethod ? importMethodLabel[item.importMethod] || '未记录' : '未记录'} · 原文来源：{sourceName(item)}</span>{url && <a href={url} target="_blank" rel="noopener noreferrer">原始链接</a>}<div className="paper-detail-attachment-actions">{item.contentRef && onOpenAttachment ? <Button size="small" onClick={() => onOpenAttachment(item.contentRef!)}>{isStoredPdfAttachment(item) ? '打开 PDF' : '打开附件'}</Button> : url ? <a href={url} target="_blank" rel="noopener noreferrer">打开原文</a> : null}{onDetachAttachment && <Button size="small" type="text" danger disabled={disabled || saving} onClick={() => Modal.confirm({ title: '从本论文移除附件？', content: `解除「${item.name}」的关联，原始文件、论文、笔记及批注保留。`, okText: '移除关联', cancelText: '取消', onOk: () => run(() => onDetachAttachment(item), '移除附件失败，请重试') })}>移除关联</Button>}</div></div>;
        }) : <p className="paper-detail-muted">仅题录，尚未添加 PDF。</p>}</div> },
        { key: 'notes', label: `笔记 ${summary.notes}`, children: <div className="paper-detail-section"><Input.TextArea disabled={disabled || saving} value={note} rows={3} placeholder="写下这篇文献的阅读笔记" onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setNote(event.target.value)} /><Button type="primary" loading={saving} disabled={disabled || saving || !note.trim()} onClick={addNote}>添加笔记</Button>{notes.map(item => <div className="paper-detail-note" key={item.id}><strong>{item.title}</strong><p>{item.content}</p></div>)}</div> },
        { key: 'annotations', label: `批注 ${summary.annotations}`, children: <div className="paper-detail-section"><p className="paper-detail-muted">PDF 批注会显示在这里，并保留页码、原文和评论。</p></div> },
        { key: 'relations', label: '关系', children: <div className="paper-detail-section"><p className="paper-detail-muted">相关文献、引用和版本关系会显示在这里。</p></div> },
      ]} />
    </aside>
  </div>;
}
