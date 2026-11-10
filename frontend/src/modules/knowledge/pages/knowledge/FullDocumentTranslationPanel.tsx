import {ReloadOutlined,UndoOutlined,DownloadOutlined,PaperClipOutlined,ColumnWidthOutlined} from "@ant-design/icons";
import type { PdfTextSelection } from "@/components/ui";
import { Alert, Button, Tooltip, message } from "antd";
import FileViewer from "@/modules/knowledge/components/FileViewer";
import { pdfArtifactContentUrl, rerenderPdfTranslation, type PdfArtifact } from "@/modules/knowledge/api/pdfArtifacts";
import {useMemo,useState,type ReactNode} from "react";
import {createTranslationAttachmentSaver,fetchTranslationFile} from '../../paper/translationAttachment';

interface Props {
  datasetId: string;
  documentId: string;
  artifact?: PdfArtifact;
  supported: boolean;
  busy: boolean;
  onTranslate: () => void;
  onRegenerate: (artifact: PdfArtifact) => void;
  onRetranslateSelection: (selection: PdfTextSelection) => void;
  onHalfWidth?: () => void;
  progressNotice?: ReactNode;
  canWrite?: boolean;
  onArtifactUpdated?: (artifact: PdfArtifact) => void;
}

export default function FullDocumentTranslationPanel(props: Props) {
  const { artifact } = props;
  const [rerendering,setRerendering]=useState(false);
  const rerender=async()=>{
    if(!artifact||rerendering)return;
    setRerendering(true);
    try{
      const updated=await rerenderPdfTranslation(props.datasetId,props.documentId,artifact.id);
      props.onArtifactUpdated?.(updated);
      message.success('已用现有译文重新排版，未重新请求翻译');
    }catch(error){message.error(error instanceof Error?error.message:'重新排版失败，原译本已保留');}
    finally{setRerendering(false);}
  };
  const [downloading,setDownloading]=useState(false);
  const [attaching,setAttaching]=useState(false);
  const [attachedId,setAttachedId]=useState('');
  const saveAttachment=useMemo(()=>artifact?createTranslationAttachmentSaver(props.datasetId,props.documentId,artifact):undefined,[props.datasetId,props.documentId,artifact]);
  const download=async()=>{
    if(!artifact||downloading)return;
    setDownloading(true);
    try{
      const file=await fetchTranslationFile(props.datasetId,props.documentId,artifact);
      const url=URL.createObjectURL(file);const anchor=document.createElement('a');
      anchor.href=url;anchor.download=file.name;anchor.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
    }catch(error){message.error(error instanceof Error?error.message:'下载失败');}
    finally{setDownloading(false);}
  };
  const attach=async()=>{
    if(!artifact||!saveAttachment||attaching)return;
    setAttaching(true);
    try{await saveAttachment();setAttachedId(artifact.id);message.success('译本已加入该文献的附件');}
    catch(error){message.error(error instanceof Error?error.message:'添加附件失败');}
    finally{setAttaching(false);}
  };
  const actions = <div className="knowledge-full-translation-actions" aria-label="译本操作">
    <Tooltip title="重新翻译"><Button type="text" size="small" aria-label="重新翻译" icon={<ReloadOutlined/>} disabled={!props.supported || props.busy || rerendering} loading={props.busy} onClick={()=>artifact&&props.onRegenerate(artifact)}/></Tooltip>
    {artifact?.has_draft && <Tooltip title="重新排版（免翻译）"><Button type="text" size="small" aria-label="重新排版（免翻译）" icon={<UndoOutlined/>} loading={rerendering} disabled={!props.canWrite || props.busy} onClick={()=>void rerender()}/></Tooltip>}
    <Tooltip title="下载译本"><Button type="text" size="small" aria-label="下载译本" icon={<DownloadOutlined/>} loading={downloading} onClick={()=>void download()}/></Tooltip>
    {artifact && /\.pdf$/i.test(artifact.filename) && <Tooltip title={attachedId===artifact.id?'已加入文献附件':'加入文献附件'}><Button type="text" size="small" aria-label={attachedId===artifact.id?'已加入文献附件':'加入文献附件'} icon={<PaperClipOutlined/>} loading={attaching} disabled={!props.canWrite||attachedId===artifact.id} onClick={()=>void attach()}/></Tooltip>}
    {props.onHalfWidth && <Tooltip title="半屏阅读"><Button type="text" size="small" aria-label="半屏阅读" icon={<ColumnWidthOutlined/>} onClick={props.onHalfWidth}/></Tooltip>}
  </div>;
  return <section className="knowledge-full-translation" aria-label="全文翻译">
    {props.progressNotice}
    {!artifact && <div className="knowledge-full-translation-toolbar"><Button type="primary" disabled={!props.supported || props.busy} loading={props.busy} onClick={props.onTranslate}>{props.busy ? "翻译进行中" : "开始全文翻译"}</Button></div>}
    {artifact && !/\.pdf$/i.test(artifact.filename) && <div className="knowledge-full-translation-toolbar">{actions}</div>}
    {artifact?.layout_preserved && <Alert type={artifact.supplemental_block_count || artifact.warning_count ? 'warning' : 'success'} showIcon message={artifact.supplemental_block_count ? '这是旧版排版结果，请点击“重新排版”生成页内译文。' : artifact.warning_count ? '正文译文已在原页排版；图表内文字和旋转标签保留原文。' : '译文已写回原页，保留原页布局。'} />}
    {rerendering && <p role="status">正在用已保存的译文重新排版，无需重新调用翻译服务。请保持此页面打开。</p>}
    {artifact ? <div className="knowledge-full-translation-viewer">
      <FileViewer pdfToolbarMode="navigation" pdfHeaderEnd={actions} key={artifact.id} file={pdfArtifactContentUrl(props.datasetId, props.documentId, artifact.id)} fileName={artifact.filename} onPdfRetranslateSelection={props.onRetranslateSelection} />
    </div> : <div className="paper-assistant-welcome">
      <h2>{props.supported ? "全文对照阅读" : "此格式暂不支持全文翻译"}</h2>
      <p>{props.supported ? props.busy ? "译本生成后会显示在这里，可在上方查看进度或取消任务。" : "选择翻译引擎和目标语言，生成全文译本。正文译文在原页内自适应排版，利用空白并调整行距和字号。图表内文字和旋转标签保留原文。原文留在主阅读区，译文在此侧栏显示。" : "仍可切换到划词翻译，翻译选中的文字。"}</p>
    </div>}
  </section>;
}
