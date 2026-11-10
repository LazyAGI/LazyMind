import type { PdfTextSelection } from "@/components/ui";
import { Button, message } from "antd";
import FileViewer from "@/modules/knowledge/components/FileViewer";
import { pdfArtifactContentUrl, type PdfArtifact } from "@/modules/knowledge/api/pdfArtifacts";
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
  return <section className="knowledge-full-translation" aria-label="全文翻译">
    {props.progressNotice}
    <div className="knowledge-full-translation-toolbar">
      <Button type={artifact ? "default" : "primary"} disabled={!props.supported || props.busy} loading={props.busy} onClick={artifact?()=>props.onRegenerate(artifact):props.onTranslate}>
        {props.busy ? "翻译进行中" : artifact ? "重新翻译" : "开始全文翻译"}
      </Button>
      {artifact && <Button loading={downloading} onClick={()=>void download()}>下载译本</Button>}
      {artifact && /\.pdf$/i.test(artifact.filename) && <Button loading={attaching} disabled={!props.canWrite||attachedId===artifact.id} onClick={()=>void attach()}>{attachedId===artifact.id?'已加入文献附件':'加入文献附件'}</Button>}
      {props.onHalfWidth && <Button onClick={props.onHalfWidth}>半屏阅读</Button>}
    </div>
    {artifact ? <div className="knowledge-full-translation-viewer">
      <FileViewer key={artifact.id} file={pdfArtifactContentUrl(props.datasetId, props.documentId, artifact.id)} fileName={artifact.filename} onPdfRetranslateSelection={props.onRetranslateSelection} />
    </div> : <div className="paper-assistant-welcome">
      <h2>{props.supported ? "全文对照阅读" : "此格式暂不支持全文翻译"}</h2>
      <p>{props.supported ? props.busy ? "译本生成后会显示在这里，可在上方查看进度或取消任务。" : "选择翻译引擎和目标语言，生成全文译本。优先保留原版式，文字放不下时自动重排，确保译文完整。原文留在主阅读区，译文在此侧栏显示。" : "仍可切换到划词翻译，翻译选中的文字。"}</p>
    </div>}
  </section>;
}
