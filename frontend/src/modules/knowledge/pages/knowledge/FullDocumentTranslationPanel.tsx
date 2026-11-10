import type { PdfTextSelection } from "@/components/ui";
import { Button, Select } from "antd";
import FileViewer from "@/modules/knowledge/components/FileViewer";
import { pdfArtifactContentUrl, type PdfArtifact } from "@/modules/knowledge/api/pdfArtifacts";
import type { ReactNode } from "react";

interface Props {
  datasetId: string;
  documentId: string;
  artifacts: PdfArtifact[];
  artifact?: PdfArtifact;
  supported: boolean;
  busy: boolean;
  onTranslate: () => void;
  onSelect: (artifact: PdfArtifact) => void;
  onRegenerate: (artifact: PdfArtifact) => void;
  onRetranslateSelection: (selection: PdfTextSelection) => void;
  onHalfWidth?: () => void;
  progressNotice?: ReactNode;
}

export default function FullDocumentTranslationPanel(props: Props) {
  const { artifact } = props;
  return <section className="knowledge-full-translation" aria-label="全文翻译">
    {props.progressNotice}
    <div className="knowledge-full-translation-toolbar">
      {props.artifacts.length > 0 && <Select
        aria-label="全文翻译版本" value={artifact?.id}
        options={props.artifacts.map((item, index) => ({value:item.id, label:`${item.target_language === "en" ? "英文" : "中文"} · ${item.provider_type === "llm" ? "大模型" : "翻译 API"} · ${index + 1}`}))}
        onChange={(id: string) => {const selected = props.artifacts.find(item => item.id === id); if (selected) props.onSelect(selected);}}
      />}
      <Button type={artifact ? "default" : "primary"} disabled={!props.supported || props.busy} loading={props.busy} onClick={props.onTranslate}>
        {props.busy ? "翻译进行中" : artifact ? "新建全文翻译" : "开始全文翻译"}
      </Button>
      {artifact && <Button disabled={props.busy} onClick={() => props.onRegenerate(artifact)}>重新生成译本</Button>}
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
