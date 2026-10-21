import { Button, Dropdown, Tooltip, type MenuProps } from "antd";
import { DeleteOutlined, EditOutlined, ExportOutlined, MoreOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { DatasetAclEnum, type Dataset } from "@/api/generated/knowledge-client";
import type { PaperCitationFormat } from "@/modules/knowledge/paper/exportLibrary";

interface Props {
  dataset: Dataset;
  onEdit: () => void;
  onAuthorize?: () => void;
  onDelete: () => void;
  onExport?: (format: PaperCitationFormat) => void;
}

export default function KnowledgeLibraryActions({ dataset, onEdit, onAuthorize, onDelete, onExport }: Props) {
  const { t } = useTranslation();
  const writable = dataset.acl?.includes(DatasetAclEnum.DatasetWrite);
  const readable = writable || dataset.acl?.includes(DatasetAclEnum.DatasetRead);
  const items: MenuProps["items"] = [
    ...(writable ? [{ key: "edit", icon: <EditOutlined />, label: t("common.edit"), onClick: onEdit }] : []),
    ...(writable && onAuthorize ? [{ key: "authorize", icon: <SafetyCertificateOutlined />, label: t("knowledge.authorize"), onClick: onAuthorize }] : []),
    ...(readable && onExport ? [{
      key: "export", icon: <ExportOutlined />, label: "导出文献",
      children: (["bib", "ris", "csv"] as const).map((format) => ({
        key: `export-${format}`, label: format === "bib" ? "BibTeX" : format.toUpperCase(), onClick: () => onExport(format),
      })),
    }] : []),
    ...(writable ? [{ type: "divider" as const }, { key: "delete", icon: <DeleteOutlined />, label: t("common.delete"), danger: true, onClick: onDelete }] : []),
  ];
  if (!items.length) return null;

  return (
    <div className="knowledge-library-actions" onClick={(event) => event.stopPropagation()}>
      <Dropdown trigger={["click"]} placement="bottomRight" menu={{ items }} overlayClassName="knowledge-library-actions-menu">
        <Tooltip title={t("common.actions")}>
          <Button type="text" className="knowledge-library-action-button" icon={<MoreOutlined />} aria-label={`${dataset.display_name || "知识库"} 的操作`} />
        </Tooltip>
      </Dropdown>
    </div>
  );
}
