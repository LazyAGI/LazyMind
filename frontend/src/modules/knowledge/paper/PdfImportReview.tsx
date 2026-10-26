import { useEffect, useMemo, useState } from 'react';
import { Alert, Checkbox, Select, type CheckboxChangeEvent } from 'antd';
import type { PaperItem } from './model';
import type { PdfPaper } from './pdfAssociation';

interface Props {
  metadata: PdfPaper;
  target?: PaperItem;
  papers: PaperItem[];
  candidateIds: string[];
  file?: File;
  fixedTarget: boolean;
  conflict: boolean;
  requiresConfirmation: boolean;
  sameVersion: boolean;
  confirmed: boolean;
  disabled: boolean;
  onTargetChange: (paperId: string | null) => void;
  onConfirmedChange: (confirmed: boolean) => void;
}

export default function PdfImportReview(props: Props) {
  const [preview, setPreview] = useState<string>();
  useEffect(() => {
    if (!props.file || typeof URL.createObjectURL !== 'function') return;
    const url = URL.createObjectURL(props.file);
    setPreview(url);
    return () => { URL.revokeObjectURL(url); setPreview(undefined); };
  }, [props.file]);
  const options = useMemo(() => [
    { value: 'new', label: '作为新论文导入' },
    ...[...props.papers].sort((a, b) => Number(props.candidateIds.includes(b.id)) - Number(props.candidateIds.includes(a.id))).map(paper => ({ value: paper.id, label: `${props.candidateIds.includes(paper.id) ? '候选 · ' : ''}${paper.title}` })),
  ], [props.papers, props.candidateIds]);

  return <div className="paper-pdf-review">
    <label className="paper-library-import-label">导入位置<Select aria-label="PDF 关联论文" value={props.target?.id || 'new'} options={options} disabled={props.disabled || props.fixedTarget} showSearch optionFilterProp="label" onChange={(value: string) => props.onTargetChange(value === 'new' ? null : value)} /></label>
    <div className="paper-pdf-comparison">
      <section><h4>文件识别结果</h4><strong>{props.metadata.title}</strong><p>{props.metadata.authors || '作者未识别'}</p><p>{props.metadata.arxiv || props.metadata.doi || '未识别出论文编号'} · {props.metadata.arxivVersion || '版本未知'}</p></section>
      <section><h4>{props.target ? '目标题录' : '将创建论文'}</h4><strong>{props.target?.title || props.metadata.title}</strong><p>{props.target?.authors || props.metadata.authors || '作者待补全'}</p><p>{props.target?.arxiv || props.target?.doi || '无论文编号'}{props.target?.arxivVersion ? ` · 题录来源 ${props.target.arxivVersion}` : ''}</p></section>
    </div>
    {props.metadata.identificationError && <Alert type="warning" showIcon message="题录验证暂未完成，请核对 PDF" description={props.metadata.identificationError} />}
    {props.conflict ? <Alert type="error" showIcon message="PDF 与当前论文身份不一致" description="请更换文件，或将这份 PDF 作为新论文导入。" /> : props.target && <Alert type={props.requiresConfirmation ? 'warning' : 'info'} showIcon message={props.requiresConfirmation ? '无法自动确认归属，请核对两侧题录与 PDF' : '已匹配已有论文，确认后添加附件'} />}
    {props.sameVersion && <Alert type="warning" showIcon message="已有相同版本附件，确认后将分别保留文件" />}
    {preview && <iframe className="paper-pdf-preview" src={`${preview}#page=1&toolbar=0`} title="PDF 首页预览" />}
    {!props.conflict && (props.requiresConfirmation || props.sameVersion) && <Checkbox checked={props.confirmed} disabled={props.disabled} onChange={(event: CheckboxChangeEvent) => props.onConfirmedChange(event.target.checked)}>我已核对，确认将这份 PDF 添加到{props.target ? `《${props.target.title}》` : '新论文'}{props.sameVersion ? '，并保留已有同版本文件' : ''}</Checkbox>}
  </div>;
}
