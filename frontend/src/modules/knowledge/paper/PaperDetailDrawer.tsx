import { useMemo, useState } from 'react';
import { Button, Input, Tabs, Tag } from 'antd';
import type { PaperLibraryDataV3, PaperRecord } from './libraryModel';
import { createNote, readingSummary } from './libraryModel';
import './PaperDetailDrawer.scss';

interface Props { paper: PaperRecord; data: PaperLibraryDataV3; onClose: () => void; onChange: (data: PaperLibraryDataV3) => void; }

export default function PaperDetailDrawer({ paper, data, onClose, onChange }: Props) {
  const [note, setNote] = useState('');
  const current = data.papers.find((item) => item.id === paper.id) || paper;
  const summary = readingSummary(data, current.id);
  const notes = useMemo(() => data.notes.filter((item) => current.noteIds.includes(item.id)), [data.notes, current.noteIds]);
  const attachments = data.attachments.filter((item) => current.attachmentIds.includes(item.id));
  const addNote = () => { if (!note.trim()) return; const next = createNote(current.id, note.trim()); onChange({ ...data, notes: [...data.notes, next], papers: data.papers.map((item) => item.id === current.id ? { ...item, noteIds: [...item.noteIds, next.id] } : item) }); setNote(''); };
  return <div className="paper-detail-mask" onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}><aside className="paper-detail-drawer" role="dialog" aria-modal="true" aria-label="文献详情">
    <header className="paper-detail-head"><div><span className="paper-detail-kicker">文献条目</span><h2>{current.title}</h2><p>{current.authors} · {current.venue} {current.year}</p></div><Button type="text" onClick={onClose}>关闭</Button></header>
    <div className="paper-detail-summary"><span>{summary.attachments} 个附件</span><span>{summary.notes} 条笔记</span><span>{summary.annotations} 条批注</span></div>
    <Tabs items={[{ key: 'overview', label: '概览', children: <div className="paper-detail-section"><h3>元数据</h3><dl><dt>DOI</dt><dd>{current.doi || '未填写'}</dd><dt>arXiv</dt><dd>{current.arxiv || '未填写'}</dd><dt>摘要</dt><dd>{current.metadata.abstract || '尚未生成摘要。导入后可继续补全。'}</dd></dl><div className="paper-detail-tags">{current.tags.map((tag) => <Tag key={tag}>{tag}</Tag>)}</div></div> }, { key: 'attachments', label: `附件 ${summary.attachments}`, children: <div className="paper-detail-section">{attachments.length ? attachments.map((item) => <div className="paper-detail-item" key={item.id}><strong>{item.name}</strong><span>{item.kind === 'pdf' ? 'PDF' : '附件'} · {item.isPrimary ? '主附件' : '补充材料'}</span></div>) : <p className="paper-detail-muted">还没有附件。可以从导入面板添加 PDF 或补充材料。</p>}</div> }, { key: 'notes', label: `笔记 ${summary.notes}`, children: <div className="paper-detail-section"><Input.TextArea value={note} rows={3} placeholder="写下这篇文献的阅读笔记" onChange={(event) => setNote(event.target.value)} /><Button type="primary" onClick={addNote}>添加笔记</Button>{notes.map((item) => <div className="paper-detail-note" key={item.id}><strong>{item.title}</strong><p>{item.content}</p></div>)}</div> }, { key: 'annotations', label: `批注 ${summary.annotations}`, children: <div className="paper-detail-section"><p className="paper-detail-muted">PDF 批注会显示在这里，并保留页码、原文和评论。</p></div> }, { key: 'relations', label: '关系', children: <div className="paper-detail-section"><p className="paper-detail-muted">相关文献、引用和版本关系会显示在这里。</p></div> }]} />
  </aside></div>;
}
