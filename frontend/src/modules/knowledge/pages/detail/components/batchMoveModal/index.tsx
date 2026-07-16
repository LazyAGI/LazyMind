import { useEffect, useMemo, useState } from "react";
import { message, Modal, TreeSelect } from "antd";
import { useTranslation } from "react-i18next";
import type { DefaultOptionType } from "antd/es/select";
import {
  DocumentServiceApi,
  TaskServiceApi,
} from "@/modules/knowledge/utils/request";
import { DocTypeEnum } from "@/api/generated/knowledge-client";
import type { BatchMoveDocument } from "../KnowledgeTable";
import { localizeErrorCode } from "@/components/request";

type TreeOption = Omit<DefaultOptionType, "label"> & {
  title: string;
  value: string;
  key: string;
  type?: string;
};

interface BatchMoveModalProps {
  open: boolean;
  datasetId: string;
  selectedFileCount: number;
  documents: BatchMoveDocument[];
  onCancel: () => void;
  onSuccess: () => void;
}

const BatchMoveModal = ({
  open,
  datasetId,
  selectedFileCount,
  documents,
  onCancel,
  onSuccess,
}: BatchMoveModalProps) => {
  const { t } = useTranslation();
  const [treeData, setTreeData] = useState<TreeOption[]>([]);
  const [selectedTarget, setSelectedTarget] = useState<TreeOption | null>(null);
  const [loading, setLoading] = useState(false);

  const rootNode = useMemo<TreeOption>(
    () => ({
      title: t("knowledge.currentKnowledgeBase"),
      value: datasetId,
      key: datasetId,
      dataset_id: datasetId,
      isLeaf: false,
    }),
    [datasetId],
  );

  useEffect(() => {
    if (!open || !datasetId) {
      return;
    }
    setSelectedTarget(null);
    DocumentServiceApi()
      .documentServiceSearchDocuments({
        dataset: datasetId,
        searchDocumentsRequest: { parent: "", page_size: 10000 },
      })
      .then((res) => {
        const children: TreeOption[] = (res?.data?.documents || [])
          .filter((doc) => doc.type === DocTypeEnum.Folder)
          .map((doc) => ({
            ...doc,
            title: doc.display_name || "",
            value: doc.document_id || "",
            key: doc.document_id || "",
            isLeaf: true,
          }))
          .filter((doc) => !!doc.value);
        setTreeData([{ ...rootNode, children }]);
      })
      .catch((error) => {
        console.error("Failed to load folder tree for batch move:", error);
        setTreeData([{ ...rootNode, children: [] }]);
      });
  }, [open, datasetId, rootNode]);

  const handleOk = async () => {
    if (!selectedFileCount || documents.length === 0) {
      message.warning(t("knowledge.selectAtLeastOneFile"));
      return;
    }
    if (!selectedTarget?.value) {
      message.warning(t("knowledge.selectMoveTarget"));
      return;
    }
    const targetPid =
      selectedTarget.value === datasetId ? "" : selectedTarget.value;
    const allAlreadyInTarget = documents.every(
      (doc) => doc.parentId === targetPid,
    );
    if (allAlreadyInTarget) {
      message.warning(t("knowledge.alreadyInTarget"));
      return;
    }
    const dataSourceTypes = new Set(
      documents.map((doc) => doc.dataSourceType).filter(Boolean),
    );
    const moveDataSourceType =
      dataSourceTypes.size === 1
        ? Array.from(dataSourceTypes)[0]
        : "DATA_SOURCE_TYPE_UNSPECIFIED";

    try {
      setLoading(true);
      const createRes = await TaskServiceApi().createTasks(datasetId, {
        parent: `datasets/${datasetId}`,
        items: [
          {
            upload_file_id: "",
            task: {
              task_type: "TASK_TYPE_MOVE",
              data_source_type: moveDataSourceType,
              document_ids: documents.map((doc) => doc.documentId),
              target_dataset_id: datasetId,
              target_pid: targetPid,
              target_path:
                selectedTarget.type === DocTypeEnum.Folder
                  ? selectedTarget.title
                  : "",
              display_name: t("knowledge.batchMoveTaskName", { count: documents.length }),
            },
          },
        ],
      });

      const tasks = createRes.data.tasks || [];
      const taskIds = tasks.map((t) => t.task_id).filter(Boolean);
      if (!taskIds.length) {
        message.error(localizeErrorCode("2000509"));
        return;
      }

      await TaskServiceApi().startTasks(datasetId, { task_ids: taskIds });
      message.info(t("knowledge.movingWait"));
      onSuccess();
      onCancel();
    } catch (error) {
      console.error("Failed to create batch move task:", error);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal
      open={open}
      title={t("knowledge.batchMoveTitle")}
      centered
      width={720}
      maskClosable={false}
      onCancel={onCancel}
      onOk={handleOk}
      okButtonProps={{ loading }}
      cancelButtonProps={{ disabled: loading }}
    >
      <div style={{ marginBottom: 16, color: "var(--color-text-description)" }}>
        {t("knowledge.selectedDocCount", { count: selectedFileCount })}
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
        <span style={{ minWidth: 60 }}>{t("knowledge.moveToLabel")}</span>
        <TreeSelect
          style={{ width: "100%" }}
          treeData={treeData}
          value={selectedTarget?.value}
          treeDefaultExpandedKeys={[datasetId]}
          placeholder={t("knowledge.inputOrSelect")}
          onSelect={(_value, option) => setSelectedTarget(option as TreeOption)}
          onChange={(_value, _label, extra) => {
            const node = extra?.triggerNode;
            if (node) {
              setSelectedTarget(node as unknown as TreeOption);
            } else {
              setSelectedTarget(null);
            }
          }}
          allowClear
        />
      </div>
    </Modal>
  );
};

export default BatchMoveModal;
