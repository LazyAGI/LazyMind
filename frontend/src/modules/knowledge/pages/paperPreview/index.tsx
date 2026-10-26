import { useNavigate } from "react-router-dom";
import PaperLibrary from "@/modules/knowledge/paper/PaperLibrary";

const previewId = "paper-preview";

export default function PaperPreviewPage() {
  const navigate = useNavigate();
  return (
    <div className="knowledge-detail-page">
      <PaperLibrary
        datasetId={previewId}
        title="论文库"
        preview
        onBack={() => navigate("/lib/knowledge/list")}
      />
    </div>
  );
}
