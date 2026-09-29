import { RAGComponentProvider } from "@/modules/modelProvider/contexts/RAGComponentContext";
import { RAGComponentNotice } from "@/modules/modelProvider/components/PythonComponentDependencies";
import { Outlet } from "react-router-dom";
import KnowledgeLayout from "./layout";
import "./style.css";

export default function KnowledgeApp() {
  return (
    <RAGComponentProvider>
      <KnowledgeLayout>
        <RAGComponentNotice />
        <Outlet />
      </KnowledgeLayout>
    </RAGComponentProvider>
  );
}
