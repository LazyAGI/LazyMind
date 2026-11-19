import type { PdfScrollSyncEndpoint } from "@/components/ui/pdfScrollSync";
import {LinkOutlined,TranslationOutlined,ReloadOutlined,DownloadOutlined,PaperClipOutlined,ColumnWidthOutlined} from "@ant-design/icons";
import type { PdfTextSelection } from "@/components/ui";
import { Button, Tooltip, message } from "antd";
import FileViewer from "@/modules/knowledge/components/FileViewer";
import { pdfArtifactContentUrl, type PdfArtifact } from "@/modules/knowledge/api/pdfArtifacts";
import {useMemo,useState,type ReactNode} from "react";
import {createTranslationAttachmentSaver,fetchTranslationFile} from '../../paper/translationAttachment';

interface Props {
  scrollSync?: PdfScrollSyncEndpoint;
  syncScroll?: boolean;
  onSyncScrollChange?: (enabled: boolean) => void;
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
}

export default function FullDocumentTranslationPanel(props: Props) {
  const { artifact } = props;
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
    {props.onSyncScrollChange && <Button type="text" size="small" aria-label="同步滚动" title={props.syncScroll ? "关闭同步滚动" : "开启同步滚动"} aria-pressed={!!props.syncScroll} className={props.syncScroll ? "is-active" : undefined} icon={<LinkOutlined/>} onClick={()=>props.onSyncScrollChange?.(!props.syncScroll)}/>}
    <Tooltip title="重新翻译"><Button type="text" size="small" aria-label="重新翻译" icon={<ReloadOutlined/>} disabled={!props.supported || props.busy} loading={props.busy} onClick={()=>artifact&&props.onRegenerate(artifact)}/></Tooltip>
    <Tooltip title="下载译本"><Button type="text" size="small" aria-label="下载译本" icon={<DownloadOutlined/>} loading={downloading} onClick={()=>void download()}/></Tooltip>
    {artifact && /\.pdf$/i.test(artifact.filename) && <Tooltip title={attachedId===artifact.id?'已加入文献附件':'加入文献附件'}><Button type="text" size="small" aria-label={attachedId===artifact.id?'已加入文献附件':'加入文献附件'} icon={<PaperClipOutlined/>} loading={attaching} disabled={!props.canWrite||attachedId===artifact.id} onClick={()=>void attach()}/></Tooltip>}
    {props.onHalfWidth && <Button type="text" size="small" aria-label="半屏阅读" icon={<ColumnWidthOutlined/>} onClick={props.onHalfWidth}/>}
  </div>;
  return <section className="knowledge-full-translation" aria-label="全文翻译">
    {props.progressNotice}
    {artifact && !/\.pdf$/i.test(artifact.filename) && <div className="knowledge-full-translation-toolbar">{actions}</div>}
    {artifact ? <div className="knowledge-full-translation-viewer">
      <FileViewer pdfSelectableText pdfScrollSync={props.scrollSync} pdfToolbarMode="navigation" pdfHeaderEnd={actions} key={artifact.id} file={pdfArtifactContentUrl(props.datasetId, props.documentId, artifact.id)} fileName={artifact.filename} onPdfRetranslateSelection={props.onRetranslateSelection} />
    </div> : <div className="knowledge-full-translation-empty">
      <div className="knowledge-full-translation-intro">
        <h2>{props.supported ? "全文对照阅读" : "此格式暂不支持全文翻译"}</h2>
        <p>{props.supported ? props.busy ? "译本正在生成，完成后即可在这里对照阅读。" : "生成全文译本，与原文并排阅读。" : "切换到划词翻译，即可翻译选中的文字。"}</p>
        {props.supported && <Button className="knowledge-full-translation-start" icon={<TranslationOutlined aria-hidden="true" />} disabled={props.busy} loading={props.busy} onClick={props.onTranslate}>{props.busy ? "翻译进行中" : "开始全文翻译"}</Button>}
      </div>
    </div>}
  </section>;
}
