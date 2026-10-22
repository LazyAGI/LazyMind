import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import PaperLibrary from "@/modules/knowledge/paper/PaperLibrary";
import { markPaperLibrary } from "@/modules/knowledge/paper/store";

const previewId = "paper-preview";

export default function PaperPreviewPage() {
  const navigate = useNavigate();
  useEffect(() => { markPaperLibrary(previewId); }, []);
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
