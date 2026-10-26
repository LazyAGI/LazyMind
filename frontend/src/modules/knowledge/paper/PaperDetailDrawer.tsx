import { useMemo, useRef, useState, type ChangeEvent } from 'react';
import { Alert, Button, Input, Tabs, Tag } from 'antd';
import type { PaperLibraryDataV3, PaperRecord, Attachment } from './libraryModel';
import { createNote, readingSummary } from './libraryModel';
import { importMethodLabel, safePaperUrl, type PaperEdits } from './model';
import type { DocumentAcademic } from '../api/paperWorkflows';
import PaperMetadataForm from '../components/PaperTasks/PaperMetadataForm';
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
}

function sourceName(attachment?: Attachment) {
  if (!attachment) return '未记录';
  if (attachment.sourceProvider === 'arxiv' || attachment.source === 'arxiv') return 'arXiv';
  if (attachment.importMethod === 'upload' || attachment.sourceProvider === 'upload') return '上传文件';
  const url = safePaperUrl(attachment.sourceUrl);
  if (url) return new URL(url).hostname;
  return attachment.sourceProvider && attachment.sourceProvider !== 'manual' ? attachment.sourceProvider : '未记录';
}

export default function PaperDetailDrawer({ paper, data, initialTab = 'overview', initialEditing = false, academic, onClose, onChange, onOpenAttachment, onEdit, onSave, editError, onRetryIdentification, disabled = false }: Props) {
  const [tab, setTab] = useState(initialTab);
  const [editing, setEditing] = useState(initialEditing);
  const [note, setNote] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const savingRef = useRef(false);
  const current = data.papers.find((item) => item.id === paper.id) || paper;
  const summary = readingSummary(data, current.id);
  const notes = useMemo(() => data.notes.filter((item) => current.noteIds.includes(item.id)), [data.notes, current.noteIds]);
  const attachments = data.attachments.filter((item) => current.attachmentIds.includes(item.id));
  const primary = attachments.find(item => item.isPrimary) || attachments[0];
  const method = primary?.importMethod || current.importMethod;
  const sourceUrl = safePaperUrl(primary?.sourceUrl);
  const identifyStatus = academic?.state.manual ? '已手动确认' : ({ pending: '待识别', processing: '识别中', succeeded: '识别成功', failed: '识别失败' }[academic?.state.status || ''] || '未记录');

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
      <header className="paper-detail-head"><div><span className="paper-detail-kicker">文献档案</span><h2>{current.title}</h2><p>{current.authors} · {current.venue} {current.year || ''}</p><div className="paper-detail-facts"><span>{current.status === 'read' ? '已读' : current.status === 'reading' ? '阅读中' : '未读'}</span></div></div><Button type="text" disabled={saving || (editing && disabled)} onClick={onClose}>关闭</Button></header>
      <div className="paper-detail-summary"><span><b>{summary.attachments}</b> 个附件</span><span><b>{summary.notes}</b> 条笔记</span><span><b>{summary.annotations}</b> 条批注</span></div>
      {error && <Alert className="paper-detail-error" type="error" showIcon message="操作未完成" description={error} />}
      <Tabs activeKey={tab} onChange={(next: string) => { if (!saving && !editing) setTab(next as PaperDetailTab); }} items={[
        { key: 'overview', label: '概览', children: <div className="paper-detail-section">
          <div className="paper-detail-section-head"><h3>论文信息</h3>{onSave && !editing && <Button size="small" disabled={disabled || saving} onClick={() => { onEdit?.(); setEditing(true); }}>编辑</Button>}</div>
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
          {academic && <div className="paper-detail-identification"><span>论文信息自动识别：{identifyStatus}</span>{academic.state.status === 'failed' && <Alert type="warning" showIcon message="论文信息提取失败，可以重试或手动编辑" description={academic.state.error} />}{academic.state.status === 'failed' && onRetryIdentification && <Button size="small" aria-label="重试自动识别" disabled={disabled || saving || editing} loading={saving} onClick={() => void run(onRetryIdentification, '自动识别重试失败，请稍后重试')}>重试自动识别</Button>}</div>}
          {current.parse === "failed" && <Alert type="error" showIcon message="正文解析失败，可通过论文菜单重新解析" description={current.processingError || academic?.processing_error} />}
          <div className="paper-detail-section-head"><h3>来源</h3></div>
          <dl><dt>导入方式</dt><dd>{method ? importMethodLabel[method] || '未记录' : '未记录'}</dd><dt>原文来源</dt><dd>{sourceName(primary)}</dd><dt>原始链接</dt><dd>{sourceUrl ? <a href={sourceUrl} target="_blank" rel="noopener noreferrer">{sourceUrl}</a> : '未记录'}</dd><dt>来源版本</dt><dd>{primary?.sourceVersion || '未记录'}</dd></dl>
        </div> },
        { key: 'attachments', label: `附件 ${summary.attachments}`, children: <div className="paper-detail-section">{attachments.length ? attachments.map(item => {
          const url = safePaperUrl(item.sourceUrl);
          return <div className="paper-detail-item" key={item.id}><strong>{item.name}</strong><span>{item.kind === 'pdf' ? 'PDF' : '附件'}{item.sourceVersion ? ` · ${item.sourceVersion}` : ''} · {item.isPrimary ? '主附件' : '其他附件'}{item.size ? ` · ${(item.size / 1024 / 1024).toFixed(1)} MB` : ''}</span><span>导入方式：{item.importMethod ? importMethodLabel[item.importMethod] || '未记录' : '未记录'} · 原文来源：{sourceName(item)}</span>{url && <a href={url} target="_blank" rel="noopener noreferrer">原始链接</a>}{item.contentRef && onOpenAttachment ? <Button size="small" onClick={() => onOpenAttachment(item.contentRef!)}>打开 PDF</Button> : url ? <a href={url} target="_blank" rel="noopener noreferrer">打开 PDF</a> : null}</div>;
        }) : <p className="paper-detail-muted">还没有附件。可以从导入面板添加 PDF 或补充材料。</p>}</div> },
        { key: 'notes', label: `笔记 ${summary.notes}`, children: <div className="paper-detail-section"><Input.TextArea disabled={disabled || saving} value={note} rows={3} placeholder="写下这篇文献的阅读笔记" onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setNote(event.target.value)} /><Button type="primary" loading={saving} disabled={disabled || saving || !note.trim()} onClick={addNote}>添加笔记</Button>{notes.map(item => <div className="paper-detail-note" key={item.id}><strong>{item.title}</strong><p>{item.content}</p></div>)}</div> },
        { key: 'annotations', label: `批注 ${summary.annotations}`, children: <div className="paper-detail-section"><p className="paper-detail-muted">PDF 批注会显示在这里，并保留页码、原文和评论。</p></div> },
        { key: 'relations', label: '关系', children: <div className="paper-detail-section"><p className="paper-detail-muted">相关文献、引用和版本关系会显示在这里。</p></div> },
      ]} />
    </aside>
  </div>;
}
