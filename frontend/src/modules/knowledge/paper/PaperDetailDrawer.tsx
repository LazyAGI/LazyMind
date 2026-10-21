import { useMemo, useState, type ChangeEvent } from 'react';
import { Button, Input, Tabs, Tag } from 'antd';
import type { PaperLibraryDataV3, PaperRecord } from './libraryModel';
import { createNote, readingSummary } from './libraryModel';
import './PaperDetailDrawer.scss';

interface Props { paper: PaperRecord; data: PaperLibraryDataV3; onClose: () => void; onChange: (data: PaperLibraryDataV3) => void; onOpenAttachment?: (documentId: string) => void; }

export default function PaperDetailDrawer({ paper, data, onClose, onChange, onOpenAttachment }: Props) {
  const [note, setNote] = useState('');
  const current = data.papers.find((item) => item.id === paper.id) || paper;
  const summary = readingSummary(data, current.id);
  const notes = useMemo(() => data.notes.filter((item) => current.noteIds.includes(item.id)), [data.notes, current.noteIds]);
  const attachments = data.attachments.filter((item) => current.attachmentIds.includes(item.id));
  const presentMetadata = [current.title, current.authors, current.year, current.doi || current.arxiv, current.metadata.abstract].filter(Boolean).length;
  const completeness = Math.round((presentMetadata / 5) * 100);
  const addNote = () => { if (!note.trim()) return; const next = createNote(current.id, note.trim()); onChange({ ...data, notes: [...data.notes, next], papers: data.papers.map((item) => item.id === current.id ? { ...item, noteIds: [...item.noteIds, next.id] } : item) }); setNote(''); };
  return <div className="paper-detail-mask" onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}><aside className="paper-detail-drawer" role="dialog" aria-modal="true" aria-label="文献详情">
    <header className="paper-detail-head"><div><span className="paper-detail-kicker">文献档案</span><h2>{current.title}</h2><p>{current.authors} · {current.venue} {current.year}</p><div className="paper-detail-facts"><span>{current.status === 'read' ? '已读' : current.status === 'reading' ? '阅读中' : '待读'}</span><span>{current.doi ? 'DOI 已识别' : current.arxiv ? 'arXiv 已识别' : '待补标识符'}</span><span>元数据 {completeness}%</span></div></div><Button type="text" onClick={onClose}>关闭</Button></header>
    <div className="paper-detail-summary"><span><b>{summary.attachments}</b> 个附件</span><span><b>{summary.notes}</b> 条笔记</span><span><b>{summary.annotations}</b> 条批注</span></div>
    <Tabs items={[{ key: 'overview', label: '概览', children: <div className="paper-detail-section"><div className="paper-detail-section-head"><h3>书目信息</h3><span>完整度 {completeness}%</span></div><dl><dt>DOI</dt><dd>{current.doi || '未填写'}</dd><dt>arXiv</dt><dd>{current.arxiv ? `${current.arxiv}${current.arxivVersion || ''}` : '未填写'}</dd><dt>摘要</dt><dd>{current.metadata.abstract || '尚未生成摘要。导入后可继续补全。'}</dd></dl><div className="paper-detail-tags">{current.tags.map((tag) => <Tag key={tag}>{tag}</Tag>)}</div></div> }, { key: 'attachments', label: `附件 ${summary.attachments}`, children: <div className="paper-detail-section">{attachments.length ? attachments.map((item) => <div className="paper-detail-item" key={item.id}><strong>{item.name}</strong><span>{item.kind === 'pdf' ? 'PDF' : '附件'}{item.sourceVersion ? ` · ${item.sourceVersion}` : ''} · {item.isPrimary ? '主附件' : '其他附件'}{item.size ? ` · ${(item.size / 1024 / 1024).toFixed(1)} MB` : ''}</span>{item.contentRef && onOpenAttachment && <Button size="small" onClick={() => onOpenAttachment(item.contentRef!)}>打开 PDF</Button>}</div>) : <p className="paper-detail-muted">还没有附件。可以从导入面板添加 PDF 或补充材料。</p>}</div> }, { key: 'notes', label: `笔记 ${summary.notes}`, children: <div className="paper-detail-section"><Input.TextArea value={note} rows={3} placeholder="写下这篇文献的阅读笔记" onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setNote(event.target.value)} /><Button type="primary" onClick={addNote}>添加笔记</Button>{notes.map((item) => <div className="paper-detail-note" key={item.id}><strong>{item.title}</strong><p>{item.content}</p></div>)}</div> }, { key: 'annotations', label: `批注 ${summary.annotations}`, children: <div className="paper-detail-section"><p className="paper-detail-muted">PDF 批注会显示在这里，并保留页码、原文和评论。</p></div> }, { key: 'relations', label: '关系', children: <div className="paper-detail-section"><p className="paper-detail-muted">相关文献、引用和版本关系会显示在这里。</p></div> }]} />
  </aside></div>;
}
