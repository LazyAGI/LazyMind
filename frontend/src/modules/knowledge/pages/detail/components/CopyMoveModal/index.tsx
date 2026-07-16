import {
  DocumentServiceApi,
  TaskServiceApi,
} from "@/modules/knowledge/utils/request";
import { CommonModal } from "@/components/ui";
import { message, TreeSelect, TreeSelectProps } from "antd";
import { DefaultOptionType } from "antd/es/select";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { localizeErrorCode } from "@/components/request";
import { TreeNode } from "../KnowledgeTable";

type ITreeData = Omit<DefaultOptionType, "label">;
interface CopyMoveModalProps {
  cancelFn: () => void;
  currentData: TreeNode;
  action: "copy" | "move";
  onSuccess?: () => void;
}

function CopyMoveModal(props: CopyMoveModalProps) {
  const { cancelFn, currentData, action, onSuccess } = props;
  const { t } = useTranslation();
  const {
    dataset_id = "",
    data_source_type = "DATA_SOURCE_TYPE_UNSPECIFIED",
    document_id = "",
    p_id,
  } = currentData ?? {};
  const [treeData, setTreeData] = useState<ITreeData[]>([]);
  const [selectTreeData, setSelectTreeData] = useState<ITreeData>({});

  console.log(selectTreeData, currentData, t("knowledge.currentSelectionParams"));

  function updateTreeData(
    list: ITreeData[],
    key: React.Key,
    children: ITreeData[],
  ): ITreeData[] {
    return list.map((node) => {
      if (node.value === key) {
        return { ...node, children };
      }
      if (node.children) {
        return {
          ...node,
          children: updateTreeData(node.children, key, children),
        };
      }
      return node;
    });
  }

  function getKnowledgeDetailFn(params: ITreeData) {
    const isRoot = params.value === dataset_id;
    return DocumentServiceApi()
      .documentServiceSearchDocuments({
        dataset: dataset_id,
        searchDocumentsRequest: {
          parent: isRoot ? "" : (params.value as string),
          page_size: 10000,
        },
      })
      .then((res) => {
        const folderArr = res?.data?.documents
          ?.filter((it) => it.type === "FOLDER")
          ?.map((k) => ({
            ...k,
            title: k.display_name,
            value: k?.document_id,
            isLeaf: true,
            dataset_id,
          }));

        setTreeData((origin) =>
          updateTreeData(origin, params.value as React.Key, folderArr),
        );
      });
  }

  useEffect(() => {
    if (!dataset_id) return;
    const rootNode: ITreeData = {
      title: t("knowledge.currentKnowledgeBase"),
      value: dataset_id,
      key: dataset_id,
      dataset_id: dataset_id,
      isLeaf: false, // Ensure root is expandable
      children: [],
    };
    // Fetch root docs
    DocumentServiceApi()
      .documentServiceSearchDocuments({
        dataset: dataset_id,
        searchDocumentsRequest: { parent: "", page_size: 10000 },
      })
      .then((docRes) => {
        const folderArr = docRes?.data?.documents
          ?.filter((it) => it.type === "FOLDER")
          ?.map((k) => ({
            ...k,
            title: k.display_name,
            value: k?.document_id,
            isLeaf: true,
            dataset_id,
          }));
        rootNode.children = folderArr;
        setTreeData([rootNode]);
      });
  }, [dataset_id]);

  const onLoadData: TreeSelectProps["loadData"] = async (params) => {
    return await getKnowledgeDetailFn(params);
  };

  function successFn() {
    const taskType = action === "move" ? "TASK_TYPE_MOVE" : "TASK_TYPE_COPY";
    const targetDatasetId = (selectTreeData?.dataset_id as string) || dataset_id;
    const targetPid =
      selectTreeData?.value === targetDatasetId
        ? ""
        : (selectTreeData?.value as string);

    TaskServiceApi()
      .createTasks(dataset_id, {
        parent: `datasets/${dataset_id}`,
        items: [
          {
            upload_file_id: "",
            task: {
              task_type: taskType,
              data_source_type,
              document_id: document_id,
              target_dataset_id: targetDatasetId,
              target_pid: targetPid,
              target_path:
                selectTreeData?.type === "FOLDER"
                  ? (selectTreeData?.display_name as string)
                  : "",
              display_name: `${action === "move" ? t("knowledge.moveTo") : t("knowledge.copyTo")} ${currentData.display_name}`,
            },
          },
        ],
      })
      .then((createRes) => {
        const tasks = createRes.data.tasks || [];
        const taskIds = tasks.map((t) => t.task_id).filter(Boolean);
        if (!taskIds.length) {
          message.error(localizeErrorCode("2000509"));
          return;
        }
        return TaskServiceApi()
          .startTasks(dataset_id, { task_ids: taskIds })
          .then(() => {
            message.info(
              action === "move"
                ? t("knowledge.movingWait")
                : t("knowledge.copyingWait"),
            );
            onSuccess?.();
            cancelFn();
          });
      })
      .catch((error) => {
        console.error(error);
      });
  }

  function renderContentFn() {
    return (
      <TreeSelect
        style={{ width: "100%" }}
        value={selectTreeData?.value}
        treeDefaultExpandedKeys={[dataset_id]}
        styles={{
          popup: { root: { maxHeight: 400, overflow: "auto" } },
        }}
        placeholder={t("knowledge.selectPlease")}
        onSelect={(_id, opt) => setSelectTreeData(opt)}
        loadData={onLoadData}
        treeData={treeData}
      />
    );
  }

  return (
    <CommonModal
      title={action === "move" ? t("knowledge.moveTo") : t("knowledge.copyTo")}
      contentText={renderContentFn()}
      successFn={successFn}
      cancelFn={cancelFn}
    />
  );
}

export default CopyMoveModal;
