import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { knowledgeResourceHref, type KnowledgeOperation } from "../utils/knowledgeOperations";

export default function KnowledgeOperationCards({ operations = [] }: { operations?: KnowledgeOperation[] }) {
  const { i18n } = useTranslation();
  const zh = i18n.language.startsWith("zh");
  const labels: Record<string, string> = zh
    ? { created: "已创建", uploaded: "已加入", moved: "已移动", deleted: "已删除", updated: "已更新", failed: "失败", unconfirmed: "结果待核实", skipped: "已跳过" }
    : { created: "Created", uploaded: "Added", moved: "Moved", deleted: "Deleted", updated: "Updated", failed: "Failed", unconfirmed: "Outcome unconfirmed", skipped: "Skipped" };
  return <div aria-live="polite" className="knowledge-operation-results">
    {operations.flatMap(operation => operation.items.map((item, index) => {
      const href = operation.action === "knowledge.resource.delete" ? undefined : knowledgeResourceHref(item);
      const processing = item.processing_status === "submitted"
        ? (zh ? "已提交后台解析" : "Background processing submitted")
        : item.processing_status === "not_requested"
          ? (zh ? "已保存原件" : "Original saved") : "";
      return <div key={`${operation.call_id}:${index}`} style={{ border: "1px solid var(--border-color, #e5e7eb)", borderRadius: 8, padding: "10px 12px", margin: "8px 0" }}>
        <div>{href ? <Link to={href}>{item.name}</Link> : item.name}</div>
        <small>{operation.action === "knowledge.document.upload" && item.resource_status === "created" ? (zh ? "已加入" : "Added") : labels[item.resource_status] || item.resource_status}{processing && ` · ${processing}`}</small>
        {item.cleanup_status && !["completed", "not_requested"].includes(item.cleanup_status) &&
          <div><small>{zh ? "后台清理状态" : "Cleanup status"}: {item.cleanup_status}</small></div>}
        {item.error?.message && <div role="status">{item.error.message}</div>}
      </div>;
    }))}
  </div>;
}
