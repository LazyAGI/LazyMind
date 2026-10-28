import {v4 as uuidv4} from 'uuid';
import { useMemo, useRef, useState, type ChangeEvent, type ReactNode } from 'react';
import { Alert, Button, Dropdown, Input, message, Modal, Select, Tabs, Tag, Tooltip } from 'antd';
import { ArrowLeftOutlined, CloseOutlined, DownOutlined, EllipsisOutlined, ExclamationCircleOutlined, FilePdfOutlined, InfoCircleOutlined, PlusOutlined, PushpinOutlined, ReadOutlined } from '@ant-design/icons';
import type { PaperLibraryData, PaperRecord, Attachment } from './libraryModel';
import { readingSummary } from './libraryModel';
import { importMethodLabel, safePaperUrl, statusLabel, type PaperEdits } from './model';
import type { PdfViewPosition } from '@/components/ui/RenderPdf';
import PaperAttachmentPreview from './PaperAttachmentPreview';
import type { DocumentAcademic } from '../api/paperWorkflows';
import PaperMetadataForm from '../components/PaperTasks/PaperMetadataForm';
import { isStoredPdfAttachment } from './paperFilters';
import PaperRelations from './PaperRelations';
import type { PaperNotification } from './usePaperNotifications';
import './PaperDetailDrawer.scss';
export type PaperDetailTab = 'overview' | 'attachments' | 'notes' | 'annotations' | 'relations';
interface Props {
    datasetId: string;
    paper: PaperRecord;
    data: PaperLibraryData;
    initialTab?: PaperDetailTab;
    initialEditing?: boolean;
    mode?: 'drawer' | 'page';
    importanceControl?: ReactNode;
    onStatusChange?: (status: PaperRecord['status']) => Promise<void>;
    onSetPrimaryAttachment?: (attachment: Attachment) => Promise<void>;
    academic?: DocumentAcademic;
    onClose: () => void;
    onAddNote: (id:string,content:string)=>Promise<void>;
    disabled?: boolean;
    onOpenAttachment?: (documentId: string) => void;
    onOpenPaper?: (id: string) => void;
    onEdit?: () => void;
    onSave?: (edits: PaperEdits) => Promise<boolean>;
    editError?: string | null;
    onRetryIdentification?: () => Promise<void>;
    onReparse?: () => void;
    reparseDisabledReason?: string;
    onAddPdf?: () => void;
    onDetachAttachment?: (attachment: Attachment) => Promise<void>;
    onNotification?: (notice: PaperNotification) => void;
    onNotificationResolved?: (id: string) => void;
}
function sourceName(attachment?: Attachment) {
    if (!attachment)
        return '未记录';
    if (attachment.sourceProvider === 'arxiv' || attachment.source === 'arxiv')
        return 'arXiv';
    if (attachment.importMethod === 'upload' || attachment.sourceProvider === 'upload')
        return '上传文件';
    const url = safePaperUrl(attachment.sourceUrl);
    if (url)
        return new URL(url).hostname;
    return attachment.sourceProvider && attachment.sourceProvider !== 'manual' ? attachment.sourceProvider : '未记录';
}
export default function PaperDetailDrawer({ datasetId, paper, data, initialTab = 'overview', initialEditing = false, mode = 'drawer', importanceControl, onStatusChange, onSetPrimaryAttachment, academic, onClose, onAddNote, onOpenAttachment, onOpenPaper, onEdit, onSave, editError, onRetryIdentification, onReparse, reparseDisabledReason, onAddPdf, onDetachAttachment, onNotification, onNotificationResolved, disabled = false }: Props) {
    const [tab, setTab] = useState<PaperDetailTab>(initialEditing ? 'overview' : initialTab);
    const [editing, setEditing] = useState(initialEditing);
    const [messageApi, messageHolder] = message.useMessage();
    const [note, setNote] = useState('');
 const noteDraft=useRef<{id:string;content:string}|null>(null);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const savingRef = useRef(false);
    const [selectedAttachmentId, setSelectedAttachmentId] = useState<string | null>(null);
    const [positions, setPositions] = useState<Record<string, PdfViewPosition>>({});
    const current = data.papers.find((item) => item.id === paper.id) || paper;
    const notes = useMemo(() => data.notes.filter((item) => current.noteIds.includes(item.id)), [data.notes, current.noteIds]);
    const attachments = data.attachments.filter((item) => current.attachmentIds.includes(item.id) && item.available !== false);
    const pdfs = attachments.filter(isStoredPdfAttachment);
    const summary = { ...readingSummary(data, current.id), attachments: attachments.length };
    const primaryPdf = pdfs.find(item => item.isPrimary) || pdfs[0];
    const primary = primaryPdf || attachments[0];
    const selectedAttachment = attachments.find(item => item.id === selectedAttachmentId) || primary;
    const method = primary?.importMethod || current.importMethod;
    const sourceUrl = safePaperUrl(primary?.sourceUrl);
    const identifyStatus = academic?.state.manual ? '已手动确认' : ({ pending: '待识别', processing: '识别中', succeeded: '识别成功', failed: '识别失败' }[academic?.state.status || ''] || '未记录');
    const readButton = <Tooltip title={!primaryPdf ? '请先在附件中添加 PDF' : undefined}><Button type="primary" aria-label="阅读 PDF" icon={<ReadOutlined aria-hidden />} disabled={!primaryPdf || !onOpenAttachment} onClick={() => onOpenAttachment?.(primaryPdf!.contentRef!)}>阅读 PDF</Button></Tooltip>;
    const run = async (action: () => Promise<void>, fallback: string) => {
        if (disabled || savingRef.current)
            return;
        savingRef.current = true;
        setSaving(true);
        setError(null);
        try {
            await action();
        }
        catch (failure) {
            setError(onNotification ? '操作尚未完成，已保留当前内容；请重试或查看通知。' : failure instanceof Error ? failure.message : fallback);
        }
        finally {
            savingRef.current = false;
            setSaving(false);
        }
    };
    const changeTab = (next: PaperDetailTab) => {
        if (editing) {
            void messageApi.warning({ key: 'paper-metadata-editing', content: '请先保存题录或取消编辑，再切换标签页。' });
            return;
        }
        if (!saving)
            setTab(next);
    };
    const addNote = () => {
        if (!note.trim())
            return;
        if(!noteDraft.current||noteDraft.current.content!==note.trim())noteDraft.current={id:uuidv4(),content:note.trim()};
        const draft=noteDraft.current;
        return run(async()=>{await onAddNote(draft.id,draft.content);noteDraft.current=null;setNote('');
        }, '笔记保存失败，请重试');
    };
    const attachmentLabel = (item: Attachment) => item.name + (item.sourceVersion && !item.name.toLowerCase().includes(item.sourceVersion.toLowerCase()) ? ` · ${item.sourceVersion}` : '');
    return <div className={mode === 'drawer' ? 'paper-detail-mask' : 'paper-detail-page'} onClick={(event) => { if (mode === 'drawer' && event.target === event.currentTarget && !saving)
        onClose(); }}>
    {messageHolder}
    <section className={mode === 'drawer' ? 'paper-detail-drawer' : 'paper-detail-content'} role={mode === 'drawer' ? 'dialog' : 'region'} aria-modal={mode === 'drawer' ? true : undefined} aria-label="文献详情">
      <header className="paper-detail-head">
        <div className="paper-detail-toolbar">
          {mode === 'page' ? <Button type="text" icon={<ArrowLeftOutlined />} disabled={saving} onClick={onClose}>返回目录</Button> : <span className="paper-detail-kicker">文献档案</span>}
          <div className="paper-detail-head-actions">
            {mode === 'drawer' && <Button type="text" aria-label="关闭" icon={<CloseOutlined />} disabled={saving || (editing && disabled)} onClick={onClose}/>}
          </div>
        </div>
        <div className="paper-detail-title"><h2>{current.metadata.title}</h2>{importanceControl}</div>
        <p>{current.metadata.authors.join('、')} · {current.metadata.venue} {current.metadata.year || ''}</p>
        <div className="paper-detail-actions">
          {readButton}
          <Button disabled={saving || editing} onClick={() => changeTab('notes')}>写笔记</Button>
          {onStatusChange ? <Select<PaperRecord['status']>
            className="paper-detail-reading-status"
            aria-label="论文阅读状态"
            value={current.status}
            disabled={disabled || saving}
            options={Object.entries(statusLabel).map(([value, label]) => ({ value, label }))}
            onChange={status => void run(() => onStatusChange(status), '阅读状态保存失败')}
          /> : <Tag>{statusLabel[current.status]}</Tag>}
          {onSave && <Button type="text" disabled={disabled || saving} onClick={() => {
            if (editing) {
                setEditing(false);
                return;
            }
            onEdit?.();
            setTab('overview');
            setEditing(true);
          }}>{editing ? '取消编辑' : '编辑题录'}</Button>}
        </div>
      </header>
      {!pdfs.length && <p className="paper-detail-read-hint"><InfoCircleOutlined /><span>请先在<button type="button" className="paper-detail-tab-link" disabled={saving || editing} onClick={() => changeTab('attachments')}>附件</button>中添加 PDF</span></p>}
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

      </div>
      {error && (onNotification ? <p className="paper-detail-error" role="status">{error}</p> : <Alert className="paper-detail-error" type="error" showIcon message="操作未完成" description={error}/>)}
      {current.associationConflict && <Alert type="warning" showIcon message="附件归属待核对" description={current.associationConflict}/>}
      <Tabs activeKey={tab} onChange={next => changeTab(next as PaperDetailTab)} items={[
            { key: 'overview', label: '概览', children: <div className="paper-detail-section">
          <div className="paper-detail-section-head">
            <h3>论文信息</h3>
            <div className="paper-detail-identification">
              {academic && <span className={academic.state.status === 'failed' && !academic.state.manual ? 'paper-detail-status-failed' : undefined} role="status" aria-label="论文信息自动识别">{identifyStatus}</span>}
              {academic?.state.status === 'failed' && onRetryIdentification && <Button size="small" aria-label="重试自动识别" disabled={disabled || saving || editing} loading={saving} onClick={() => void run(onRetryIdentification, '自动识别重试失败，请稍后重试')}>重试自动识别</Button>}
            </div>
          </div>
          {editing ? <>
            {editError && (onNotification ? <p role="status">{editError}</p> : <Alert type="error" showIcon message="题录尚未保存" description={editError}/>)}
            <PaperMetadataForm value={{...current.metadata,authors:current.metadata.authors.join('; '),doi:current.metadata.identifiers.doi,arxiv_id:current.metadata.identifiers.arxiv,tags:current.tags.join(';')}} disabled={disabled || saving} includeTags onSave={value => run(async () => {
                            const saved = await onSave!({ title: value.title, authors: value.authors || '', venue: value.venue || '', year: value.year || 0, doi: value.doi, arxiv: value.arxiv_id, abstract: value.abstract, tags: (value.tags || '').split(/[;；]/) });
                            if (saved)
                                setEditing(false);
                        }, '论文信息保存失败，请重试')}/>
            <Button disabled={disabled || saving} onClick={() => setEditing(false)}>取消编辑</Button>
          </> : <>
            <dl><dt>标题</dt><dd>{current.metadata.title}</dd><dt>作者</dt><dd>{current.metadata.authors.join('、') || '未填写'}</dd><dt>年份</dt><dd>{current.metadata.year || '未填写'}</dd><dt>会议／期刊</dt><dd>{current.metadata.venue || '未填写'}</dd><dt>DOI</dt><dd>{current.metadata.identifiers.doi || '未填写'}</dd><dt>arXiv</dt><dd>{current.metadata.identifiers.arxiv ? `${current.metadata.identifiers.arxiv}${current.arxivVersion || ''}` : '未填写'}</dd><dt>摘要</dt><dd>{current.metadata.abstract ?? '未填写'}</dd></dl>
            <div className="paper-detail-tags">{current.tags.filter(tag => tag !== 'paper' && !tag.startsWith('arxiv:')).map(tag => <Tag key={tag}>{tag}</Tag>)}</div>
          </>}
          <div className="paper-detail-section-head"><h3>全文附件</h3></div>
          <p className="paper-detail-muted">{pdfs.length ? <button type="button" className="paper-detail-tab-link" onClick={() => changeTab('attachments')}>PDF · {pdfs.length} 份，查看附件与版本</button> : attachments.length ? '尚未添加入库 PDF，可在附件页查看其他材料。' : '仅题录，尚未添加 PDF。'}</p>
          <div className="paper-detail-section-head"><h3>来源</h3></div>
          <dl><dt>导入方式</dt><dd>{method ? importMethodLabel[method] || '未记录' : '未记录'}</dd><dt>原文来源</dt><dd>{sourceName(primary)}</dd><dt>原始链接</dt><dd>{sourceUrl ? <a href={sourceUrl} target="_blank" rel="noopener noreferrer">{sourceUrl}</a> : '未记录'}</dd><dt>来源版本</dt><dd>{primary?.sourceVersion || '未记录'}</dd></dl>
        </div> },
            { key: 'attachments', label: `附件 ${summary.attachments}`, children: selectedAttachment ? <div className="paper-attachment-frame">
              <div className="paper-attachment-bar">
                <div className="paper-attachment-identity"><span className="paper-attachment-icon"><FilePdfOutlined /></span><div className="paper-attachment-name">
                  <div className="paper-attachment-name-line">{attachments.length > 1 ? <label className="paper-attachment-selector"><select aria-label="选择预览附件与版本" disabled={saving || editing} value={selectedAttachment.id} onChange={event => setSelectedAttachmentId(event.target.value)}>{attachments.map(item => <option key={item.id} value={item.id}>{attachmentLabel(item)}</option>)}</select><DownOutlined /></label> : <strong>{attachmentLabel(selectedAttachment)}</strong>}{selectedAttachment.id === primaryPdf?.id && <span className="paper-attachment-primary"><PushpinOutlined />主附件</span>}</div>
                  <span className="paper-attachment-meta">{[sourceName(selectedAttachment), !selectedAttachment.sourceVersion ? '版本未标注' : '', selectedAttachment.size ? `${(selectedAttachment.size / 1024 / 1024).toFixed(1)} MB` : ''].filter(Boolean).join(' · ')}</span>
                </div></div>
                <div className="paper-attachment-controls">
                  {selectedAttachment.contentRef && onOpenAttachment ? <Button onClick={() => onOpenAttachment(selectedAttachment.contentRef!)}>阅读 ↗</Button> : safePaperUrl(selectedAttachment.sourceUrl) ? <Button href={safePaperUrl(selectedAttachment.sourceUrl)} target="_blank" rel="noopener noreferrer">打开原文 ↗</Button> : null}
                  {onSetPrimaryAttachment && isStoredPdfAttachment(selectedAttachment) && selectedAttachment.id !== primaryPdf?.id && <Button type="text" aria-label="设为主附件" icon={<PushpinOutlined aria-hidden />} disabled={disabled || saving || editing} onClick={() => void run(() => onSetPrimaryAttachment(selectedAttachment), '主附件设置失败，请重试')}>设为主附件</Button>}
                  {(onAddPdf || onDetachAttachment) && <Dropdown trigger={['click']} menu={{ items: [
                    ...(onAddPdf ? [{ key: 'add', label: '添加 PDF', icon: <PlusOutlined aria-hidden />, disabled: disabled || saving || editing, onClick: onAddPdf }] : []),
                    ...(onDetachAttachment ? [{ key: 'detach', label: '解除论文关联', disabled: disabled || saving || editing, onClick: () => Modal.confirm({ title: '解除与这篇论文的关联？', content: `「${selectedAttachment.name}」将从本论文的附件中移出。文件仍保留在知识库中，论文和笔记保留。`, okText: '解除关联', cancelText: '取消', onOk: () => run(() => onDetachAttachment(selectedAttachment), '解除关联失败，请重试') }) }] : []),
                  ] }}><Button type="text" icon={<EllipsisOutlined />} aria-label="附件操作" /></Dropdown>}
                </div>
              </div>
              {selectedAttachment.contentRef ? <PaperAttachmentPreview key={`${datasetId}:${selectedAttachment.contentRef}`} datasetId={datasetId} attachment={selectedAttachment} position={positions[selectedAttachment.contentRef]} onPositionChange={position => setPositions(current => ({ ...current, [selectedAttachment.contentRef!]: position }))} /> : <p className="paper-attachment-preview-status">此附件尚无入库原文件，可通过原始链接阅读。</p>}
            </div> : <div className="paper-detail-section"><p className="paper-detail-muted">仅题录，尚未添加 PDF。</p>{onAddPdf && <Button disabled={disabled || saving || editing} onClick={onAddPdf}>添加 PDF</Button>}</div> },
            { key: 'notes', label: `笔记 ${summary.notes}`, children: <div className="paper-detail-section"><p className="paper-detail-muted">这篇论文的所有 PDF 附件共用笔记。</p><Input.TextArea disabled={disabled || saving} value={note} rows={3} placeholder="写下这篇文献的阅读笔记" onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setNote(event.target.value)}/><Button type="primary" loading={saving} disabled={disabled || saving || !note.trim()} onClick={addNote}>添加笔记</Button>{notes.map(item => <div className="paper-detail-note" key={item.id}><strong>{item.title}</strong><p>{item.content}</p></div>)}</div> },
            { key: 'annotations', label: '批注', children: <div className="paper-detail-section"><p className="paper-detail-muted">PDF 批注会显示在这里，并保留页码、原文和评论。</p></div> },
            { key: 'relations', label: '关系', children: <PaperRelations key={current.id} datasetId={datasetId} paper={current} data={data} attachments={attachments} onOpenPaper={onOpenPaper} onAttachments={() => changeTab('attachments')} onNotification={onNotification} onNotificationResolved={onNotificationResolved}/> },
        ]}/>
    </section>
  </div>;
}
