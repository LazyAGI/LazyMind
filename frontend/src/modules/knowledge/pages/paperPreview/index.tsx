import { Navigate } from "react-router-dom";

// Keep saved prototype links usable after the persistent paper library replaces it.
export default function PaperPreviewPage() {
  return <Navigate to="/lib/knowledge/list" replace />;
}
