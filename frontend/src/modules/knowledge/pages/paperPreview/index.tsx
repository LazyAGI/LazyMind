import { useEffect } from "react";
import { message } from "antd";
import { useNavigate } from "react-router-dom";
import { DetailPageHeader } from "@/components/ui";
import PaperLibrary from "@/modules/knowledge/paper/PaperLibrary";
import { markPaperLibrary } from "@/modules/knowledge/paper/store";

const previewId = "paper-preview";

export default function PaperPreviewPage() {
  const navigate = useNavigate();
  useEffect(() => { markPaperLibrary(previewId); }, []);
  return (
    <div className="knowledge-detail-page">
      <DetailPageHeader
        title={<strong>AI 论文</strong>}
        breadcrumbs={[{ title: "知识库", href: "/lib/knowledge/list" }, { title: "论文集" }]}
        onBack={() => navigate("/lib/knowledge/list")}
      />
      <PaperLibrary datasetId={previewId} preview onUploadPdf={() => message.info("预览里先用题录导入。正式论文集里，这里会打开 PDF 上传。")} />
    </div>
  );
}
