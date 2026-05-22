import {
  useEffect,
  useRef,
  useState,
  useImperativeHandle,
  forwardRef,
} from "react";
import {
  Dataset,
  Doc,
  DocDataSourceTypeEnum,
  DocDocumentStageEnum,
  DocTypeEnum,
  JobJobTypeEnum,
} from "@/api/generated/knowledge-client";
import {
  DocumentServiceApi,
  JobServiceApi,
  normalizeProxyableUrl,
} from "@/modules/knowledge/utils/request";
import {
  Button,
  Checkbox,
  message,
  Modal,
  Dropdown,
  Tooltip,
  Table,
  TablePaginationConfig,
  Tag,
} from "antd";
import type { MenuProps } from "antd";
import moment from "moment";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import {
  FOLDER_NAME_REG,
  TIME_FORMAT,
} from "@/modules/knowledge/constants/common";
import {
  BookOutlined,
  FolderOutlined,
  FolderOpenOutlined,
  CaretDownOutlined,
  CaretRightOutlined,
  DownOutlined,
  EditFilled,
} from "@ant-design/icons";
import { useNavigate } from "react-router-dom";
import { cloneDeep } from "lodash";
import { useTranslation } from "react-i18next";
import { ColumnType } from "antd/es/table";
import { AgentAppsAuth } from "@/components/auth";
import FileUtils from "@/modules/knowledge/utils/file";
import { isDocumentDetailUnsupported } from "@/modules/knowledge/utils/document";
import RenameModel, {
  RenameFormItem,
  RenameModalRef,
} from "@/modules/knowledge/components/RenameModel";
import RestartKnowledgeModal, {
  type IRestartKnowledgeProps,
} from "../RestartKnowledgeModal";
import TreeUtils from "@/modules/knowledge/utils/tree";
import UIUtils from "@/modules/knowledge/utils/ui";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import type { Job } from "@/api/generated/knowledge-client";

import CopyMoveModal from "../CopyMoveModal";
import EditTags from "./editTags";
import BatchEditTags from "../batchEditTags";
import BatchMoveModal from "../batchMoveModal";

export interface TreeNode extends Doc {
  key: string;
  title: string;
  children?: TreeNode[];
  isLeaf?: boolean;
  level: number;
  loaded?: boolean;
  document_id?: string;
  document_stage: DocDocumentStageEnum;
}

export interface IKnowledgeListRef {
  getTableData: (params?: {
    pId: string;
    level: number;
    parentNode?: TreeNode;
  }) => void;
  updateDocument: (params?: {
    documentId: string;
    level?: number;
    parentNode?: TreeNode;
  }) => void;
  treeData: TreeNode[];
  deleteKnowledge: () => void;
  downloadCheckedKnowledge: () => void;
  restartCheckedKnowledge: () => void;
  openBatchEditTags: () => void;
  openBatchMove: () => void;
  refresh: (keyword: string) => void;
}

export interface BatchMoveDocument {
  documentId: string;
  parentId: string;
  dataSourceType: string;
}

interface Props {
  detail: Dataset;
  onImportKnowledge: (data: { p_id?: string; targetPath?: string }) => void;
  getImportingTotal: () => void;
  getDetail: () => void;
}

const DocumentStageEnum = {
  WAITING: "knowledge.pending",
  WORKING: "knowledge.processing",
  SUCCESS: "common.success",
  FAILED: "common.failed",
  CANCELED: "knowledge.stageCanceled",
  DELETING: "knowledge.stageDeleting",
  DELETED: "knowledge.stageDeleted",

  [DocDocumentStageEnum.DocumentUploaded]: "knowledge.pending",
  [DocDocumentStageEnum.DocumentQueued]: "knowledge.pending",
  [DocDocumentStageEnum.DocumentCrawlingQueued]: "knowledge.pending",
  [DocDocumentStageEnum.DocumentParsing]: "knowledge.processing",
  [DocDocumentStageEnum.DocumentCrawling]: "knowledge.processing",
  [DocDocumentStageEnum.DocumentParseSuccessfully]: "common.success",
  [DocDocumentStageEnum.DocumentParsingFailed]: "common.failed",
  [DocDocumentStageEnum.DocumentCrawlingFailed]: "common.failed",
  [DocDocumentStageEnum.DocumentFailed]: "common.failed",
  [DocDocumentStageEnum.DocumentParsingCancelled]: "knowledge.stageCanceled",
} as const;

const DocumentStageTagColorMap = {
  WAITING: "default",
  WORKING: "processing",
  SUCCESS: "success",
  FAILED: "error",
  CANCELED: "warning",
  DELETING: "processing",
  DELETED: "default",

  [DocDocumentStageEnum.DocumentUploaded]: "default",
  [DocDocumentStageEnum.DocumentQueued]: "default",
  [DocDocumentStageEnum.DocumentCrawlingQueued]: "default",
  [DocDocumentStageEnum.DocumentParsing]: "processing",
  [DocDocumentStageEnum.DocumentCrawling]: "processing",
  [DocDocumentStageEnum.DocumentParseSuccessfully]: "success",
  [DocDocumentStageEnum.DocumentParsingFailed]: "error",
  [DocDocumentStageEnum.DocumentCrawlingFailed]: "error",
  [DocDocumentStageEnum.DocumentFailed]: "error",
  [DocDocumentStageEnum.DocumentParsingCancelled]: "warning",
} as const;

const KnowledgeTable = forwardRef<IKnowledgeListRef, Props>((props, ref) => {
  const { detail, onImportKnowledge, getDetail, getImportingTotal } = props;
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [tableData, setTableData] = useState<TreeNode[]>([]);
  const [selectedRowKeys, setSelectedRowKeys] = useState<string[]>([]);
  const [expandedRowKeys, setExpandedRowKeys] = useState<string[]>([]);
  const [currentNode, setCurrentNode] = useState<TreeNode | null>(null);
  const knowledgeRenameRef = useRef<RenameModalRef>(null);
  const restartKnowledgeRef = useRef<IRestartKnowledgeProps>(null);
  const [keyword, setKeyword] = useState("");
  const [pagination, setPagination] = useState<TablePaginationConfig>({
    current: 1,
    pageSize: 10,
    total: 0,
  });

  const [showCopyModal, setShowCopyModal] = useState(false);
  const [currentDocInfo, setCurrentDocInfo] = useState({});
  const [action, setAction] = useState<"copy" | "move">("move");
  const [showTagEditModal, setShowTagEditModal] = useState(false);
  const [tagEditRecord, setTagEditRecord] = useState<TreeNode | null>(null);

  const [batchTagEditState, setBatchTagEditState] = useState({
    showModal: false,
    documentIds: [] as string[],
    folderIds: [] as string[],
    selectedFileCount: 0,
  });
  const [batchMoveState, setBatchMoveState] = useState({
    showModal: false,
    documents: [] as BatchMoveDocument[],
    selectedFileCount: 0,
  });

  const hasWritePermission = useDatasetPermissionStore((state) =>
    state.hasWritePermission(),
  );
  const hasOnlyReadPermission = useDatasetPermissionStore((state) =>
    state.hasOnlyReadPermission(),
  );
  const hasUploadPermission = useDatasetPermissionStore((state) =>
    state.hasUploadPermission(),
  );

  const getAllChildrenKeys = (node: TreeNode): string[] => {
    const keys: string[] = [];
    if (node.children) {
      node.children.forEach((child) => {
        if (child.document_id) {
          keys.push(child.document_id);
          keys.push(...getAllChildrenKeys(child));
        }
      });
    }
    return keys;
  };

  
  const isFolderFullySelected = (node: TreeNode): boolean => {
    const childKeys = getAllChildrenKeys(node);
    if (childKeys.length === 0) {
      return selectedRowKeys.includes(node.document_id || "");
    }
    return childKeys.every((k) => selectedRowKeys.includes(k));
  };

  const getAllKeys = (data: TreeNode[]): string[] => {
    const keys: string[] = [];
    data.forEach((item) => {
      if (item.document_id) {
        keys.push(item.document_id);
        keys.push(...getAllChildrenKeys(item));
      }
    });
    return keys;
  };

  const handleSelectAll = (checked: boolean) => {
    if (checked) {
      setSelectedRowKeys(getAllKeys(tableData));
    } else {
      setSelectedRowKeys([]);
    }
  };

  const handleSelect = (record: TreeNode, selected: boolean) => {
    if (!record.document_id) return;

    let keys = [...selectedRowKeys];
    const selfAndChildren = [record.document_id, ...getAllChildrenKeys(record)];

    if (selected) {
      keys = [...new Set([...keys, ...selfAndChildren])];
    } else {
      keys = keys.filter((k) => !selfAndChildren.includes(k));
      const ancestorIds = TreeUtils.findAncestorFolderIds(
        tableData,
        record.document_id!,
      );
      keys = keys.filter((k) => !ancestorIds.includes(k));
    }
    setSelectedRowKeys(keys);
  };

  const handleExpand = (expanded: boolean, record: TreeNode) => {
    if (!record.document_id) {
      return;
    }

    let newExpandedRowKeys = [...expandedRowKeys];

    if (expanded) {
      newExpandedRowKeys.push(record.document_id);
      getDocumentData({
        pId: record.document_id,
        level: record.level + 1,
        parentNode: record,
      });
    } else {
      newExpandedRowKeys = newExpandedRowKeys.filter(
        (key) => key !== record.document_id,
      );
    }

    setExpandedRowKeys(newExpandedRowKeys);
  };

  const handleDelete = (records: TreeNode[]) => {
    if (records.length === 0) {
      message.warning(t("knowledge.selectAtLeastOneFile"));
      return;
    }
    Modal.confirm({
      title: t("knowledge.deleteDoc"),
      content:
        records.length > 1
          ? t("knowledge.deleteDocConfirm")
          : t("knowledge.deleteDocConfirmWithName", {
              name: `【${records[0].display_name}】`,
            }),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      onOk: () => {
        DocumentServiceApi()
          .documentServiceBatchDeleteDocument({
            dataset: detail?.dataset_id || "",
            batchDeleteDocumentRequest: {
              parent: "",
              names: records.map((item) => item.document_id!),
            },
          })
          .then(() => {
            message.success(t("knowledge.deleteDocSuccess"));
            setSelectedRowKeys([]);
            setExpandedRowKeys([]);
            getDocumentData({ pId: "", level: 0, page: 1 });
            getDetail();
          })
          .catch(() => {
            message.error(t("knowledge.deleteDocFailed"));
          });
      },
    });
  };

  const onRename = (data: RenameFormItem): Promise<void> => {
    if (!currentNode) {
      return Promise.resolve();
    }
    DocumentServiceApi()
      .documentServiceUpdateDocument({
        dataset: detail.dataset_id!,
        document: currentNode.document_id!,
        doc: {
          display_name:
            data.name +
            (currentNode.data_source_type === DocDataSourceTypeEnum.LocalFile
              ? FileUtils.getSuffix(currentNode.display_name || "", true)
              : ""),
          tags: data.tags,
        },
      })
      .then(() => {
        if (!currentNode.isLeaf && currentNode.document_id) {
          setExpandedRowKeys((prev) =>
            prev.filter((key) => key !== currentNode.document_id),
          );
        }
        if (currentNode.isLeaf) {
          if (currentNode.p_id) {
            setExpandedRowKeys((prev) =>
              prev.filter((key) => key !== currentNode.p_id),
            );
          }
          getDocumentData({
            pId: "",
            level: 0,
            page: pagination.current,
            pageSize: pagination.pageSize,
          });
          return;
        }
        getDocumentData({
          pId: currentNode.p_id || "",
          level: currentNode.level,
        });
      })
      .finally(() => {
        setCurrentNode(null);
      });
    return Promise.resolve();
  };

  const handleOpenTagEdit = (record: TreeNode) => {
    setTagEditRecord(record);
    setShowTagEditModal(true);
  };

  const handleCloseTagEdit = () => {
    setShowTagEditModal(false);
    setTagEditRecord(null);
  };

  const handleTagEditSuccess = () => {
    if (tagEditRecord) {
      if (
        tagEditRecord.type === DocTypeEnum.Folder &&
        tagEditRecord.document_id &&
        expandedRowKeys.includes(tagEditRecord.document_id)
      ) {
        setExpandedRowKeys((prev) =>
          prev.filter((k) => k !== tagEditRecord.document_id),
        );
      }

      if (tagEditRecord.p_id) {
        const parentNode = TreeUtils.findNode(
          tableData,
          (node: TreeNode) => node.document_id === tagEditRecord.p_id,
        );
        getDocumentData({
          pId: tagEditRecord.p_id,
          level: tagEditRecord.level,
          parentNode: parentNode || undefined,
        });
        return;
      }
      getDocumentData({
        pId: "",
        level: 0,
        page: pagination.current,
        pageSize: pagination.pageSize,
      });
    }
  };

  const resolveBatchSelectionMeta = async (ids: string[]) => {
    const datasetId = detail.dataset_id || "";
    const folderIds = new Set<string>();
    const directDocumentIds = new Set<string>();
    const leafMap = new Map<string, BatchMoveDocument>();

    const appendLeafDoc = (
      doc: Partial<TreeNode>,
      isDirectSelection = false,
    ) => {
      const documentId = doc.document_id || "";
      if (!documentId) {
        return;
      }
      if (isDirectSelection) {
        directDocumentIds.add(documentId);
      }
      leafMap.set(documentId, {
        documentId,
        parentId: doc.p_id || "",
        dataSourceType: (doc.data_source_type as string) || "",
      });
    };

    const classifySelected = async (id: string) => {
      const node = TreeUtils.findNode(
        tableData,
        (n: TreeNode) => n.document_id === id,
      ) as TreeNode | undefined;
      const type = node?.type;
      if (type) {
        if (type === DocTypeEnum.Folder) {
          folderIds.add(id);
        } else {
          appendLeafDoc(node, true);
        }
        return;
      }
      try {
        const res = await DocumentServiceApi().documentServiceGetDocument({
          dataset: datasetId,
          document: id,
        });
        const doc = res.data as unknown as TreeNode;
        if (doc?.type === DocTypeEnum.Folder) {
          folderIds.add(id);
          return;
        }
        appendLeafDoc(doc, true);
      } catch (e) {
        console.error("Failed to load document:", e);
      }
    };

    await Promise.all(ids.map(classifySelected));

    const visitedFolder = new Set<string>();
    const folderQueue = Array.from(folderIds);
    for (let i = 0; i < folderQueue.length; i += 1) {
      const folderId = folderQueue[i];
      if (visitedFolder.has(folderId)) {
        continue;
      }
      visitedFolder.add(folderId);
      try {
        const res = await DocumentServiceApi().documentServiceSearchDocuments({
          dataset: datasetId,
          searchDocumentsRequest: {
            parent: "",
            p_id: folderId,
            keyword: "",
            page_size: 10000,
          },
        });
        (res.data.documents || []).forEach((doc) => {
          const id = doc.document_id || "";
          if (!id) {
            return;
          }
          if (doc.type === DocTypeEnum.Folder) {
            if (!visitedFolder.has(id)) {
              folderQueue.push(id);
            }
            return;
          }
          appendLeafDoc(doc as unknown as TreeNode);
        });
      } catch (e) {
        console.error("Failed to list folder documents:", e);
      }
    }

    return { folderIds, directDocumentIds, leafMap };
  };

  const resolveBatchEditTagsMeta = async (ids: string[]) => {
    const { folderIds, directDocumentIds, leafMap } =
      await resolveBatchSelectionMeta(ids);

    return {
      selectedFileCount: leafMap.size,
      folderIds: Array.from(folderIds),
      documentIds: Array.from(directDocumentIds),
    };
  };

  const doOpenBatchEditTags = async () => {
    if (selectedRowKeys.length === 0) {
      message.warning(t("knowledge.selectAtLeastOneFile"));
      return;
    }
    const key = "batchEditTagsResolving";
    message.open({
      key,
      type: "loading",
      content: t("knowledge.countingSelectedFiles"),
      duration: 0,
    });
    try {
      const { selectedFileCount, folderIds, documentIds } =
        await resolveBatchEditTagsMeta(selectedRowKeys);
      if (selectedFileCount === 0) {
        message.warning(t("knowledge.noOperableFiles"));
        return;
      }
      setBatchTagEditState({
        showModal: true,
        documentIds,
        folderIds,
        selectedFileCount,
      });
    } finally {
      message.destroy(key);
    }
  };

  const resolveBatchMoveMeta = async (ids: string[]) => {
    const { leafMap } = await resolveBatchSelectionMeta(ids);

    return {
      selectedFileCount: leafMap.size,
      documents: Array.from(leafMap.values()),
    };
  };

  const doOpenBatchMove = async () => {
    if (!hasWritePermission) {
      message.warning(t("knowledge.noWritePermission"));
      return;
    }
    if (selectedRowKeys.length === 0) {
      message.warning(t("knowledge.selectAtLeastOneFile"));
      return;
    }
    const key = "batchMoveResolving";
    message.open({
      key,
      type: "loading",
      content: t("knowledge.countingSelectedFiles"),
      duration: 0,
    });
    try {
      const { selectedFileCount, documents } =
        await resolveBatchMoveMeta(selectedRowKeys);
      if (selectedFileCount === 0) {
        message.warning(t("knowledge.noMovableFiles"));
        return;
      }
      setBatchMoveState({
        showModal: true,
        documents,
        selectedFileCount,
      });
    } finally {
      message.destroy(key);
    }
  };

  const onDownload = async (record: TreeNode) => {
    const downloadFileUrl = (record as any).download_file_url || record.uri || "";

    if (!downloadFileUrl) {
      message.error(t("knowledge.fileUrlMissing"));
      return;
    }

    const fileUrl = `${window.location.origin}/api/core${downloadFileUrl}`;

    const resolvedFileUrl = normalizeProxyableUrl(fileUrl);

    const loadingKey = "download-" + record.document_id;
    message.loading({ content: t("knowledge.downloading"), key: loadingKey });

    try {
      const authHeaders = AgentAppsAuth.getAuthHeaders();
      const headers = new Headers();

      Object.entries(authHeaders).forEach(([key, value]) => {
        if (value) {
          headers.set(key, value);
        }
      });

      const response = await fetch(resolvedFileUrl, {
        headers: headers.keys().next().done ? undefined : headers,
      });

      if (!response.ok) {
        throw new Error(`下载失败: ${response.status}`);
      }

      const blob = await response.blob();
      const blobUrl = URL.createObjectURL(blob);

      const link = document.createElement("a");
      link.href = blobUrl;
      link.download = record.display_name || "download";
      document.body.appendChild(link);
      link.click();
      document.body.removeChild(link);

      setTimeout(() => URL.revokeObjectURL(blobUrl), 100);

      message.success({ content: t("knowledge.downloadSuccess"), key: loadingKey });
    } catch (error) {
      console.error("下载失败:", error);
      message.error({
        content:
          error instanceof Error ? error.message : t("knowledge.downloadFailedRetry"),
        key: loadingKey,
      });
    }
  };

  const columns = [
    {
      title: (
        <div className="flex items-center">
          <Button type="link" size="small" className="mr-2" icon={<span />} />
          <Checkbox
            checked={
              selectedRowKeys.length > 0 &&
              selectedRowKeys.length === getAllKeys(tableData).length
            }
            indeterminate={
              selectedRowKeys.length > 0 &&
              selectedRowKeys.length < getAllKeys(tableData).length
            }
            onChange={(e) => handleSelectAll(e.target.checked)}
          />
          <span style={{ marginLeft: "12px" }}>{t("knowledge.knowledge")}</span>
        </div>
      ),
      dataIndex: "display_name",
      width: 300,
      render: (text: string, record: TreeNode) => {
        const isFolder = record.type === DocTypeEnum.Folder;
        const isExpanded = expandedRowKeys.includes(record.document_id || "");

        return (
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              paddingLeft: record.level * 20,
              flexWrap: 'nowrap',
              width: '100%',
            }}
          >
            <Button
              icon={
                isExpanded ? (
                  <CaretDownOutlined />
                ) : isFolder ? (
                  <CaretRightOutlined />
                ) : (
                  <span />
                )
              }
              type="text"
              size="small"
              onClick={() => {
                if (record.type === DocTypeEnum.Folder) {
                  handleExpand(!isExpanded, record);
                  return;
                }
              }}
            />
            <Checkbox
              checked={
                isFolder
                  ? isFolderFullySelected(record)
                  : selectedRowKeys.includes(record.document_id || "")
              }
              onChange={(e) => handleSelect(record, e.target.checked)}
            />
            <Button
              size="small"
              type="link"
              icon={
                isFolder ? (
                  isExpanded ? (
                    <FolderOpenOutlined />
                  ) : (
                    <FolderOutlined />
                  )
                ) : (
                  <BookOutlined />
                )
              }
            />
            <Tooltip title={text} placement="topLeft">
              <span
                style={{
                  flex: '1 1 0',
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  cursor: "pointer",
                  color: "#1890ff",
                  minWidth: 0,
                  display: 'inline-block',
                }}
                onClick={() => {
                  if (record.type === DocTypeEnum.Folder) {
                    handleExpand(!isExpanded, record);
                    return;
                  }
                  if (isDocumentDetailUnsupported(record.display_name)) {
                    message.info(t("knowledge.documentDetailUnsupported"));
                    return;
                  }
                  navigate({
                    pathname: `/lib/knowledge/knowledge/${detail.dataset_id}/${record.document_id}`,
                  });
                }}
              >
                {text}
              </span>
            </Tooltip>
          </div>
        );
      },
    },
    {
      title: t("knowledge.tags"),
      dataIndex: "tags",
      width: 120,
      render: (tags: string[], record: TreeNode) => {
        if (record.type === DocTypeEnum.Folder) {
          return <span>-</span>;
        }
        if (!tags || tags.length === 0) {
          return (
            <div style={{ display: "flex", alignItems: "center", gap: "4px" }}>
              <span>-</span>
              {hasWritePermission && (
                <Button
                  type="text"
                  size="small"
                  icon={<EditFilled style={{ color: "#1890ff" }} />}
                  onClick={(e) => {
                    e.stopPropagation();
                    handleOpenTagEdit(record);
                  }}
                  style={{ padding: 0, minWidth: "auto", height: "auto" }}
                />
              )}
            </div>
          );
        }
        return (
          <div style={{ display: "flex", alignItems: "center", gap: "4px" }}>
            <div
              style={{
                display: "flex",
                gap: "4px",
                overflowX: "auto",
                overflowY: "hidden",
                maxWidth: "100%",
                paddingBottom: "2px",
                WebkitOverflowScrolling: "touch",
                flex: 1,
              }}
              className="tags-scroll-container"
            >
              {tags.map((tag) => (
                <Tag
                  key={tag}
                  style={{ flexShrink: 0, margin: 0, whiteSpace: "nowrap" }}
                >
                  {tag}
                </Tag>
              ))}
            </div>
            {hasWritePermission && (
              <Button
                type="text"
                size="small"
                icon={<EditFilled style={{ color: "#1890ff" }} />}
                onClick={(e) => {
                  e.stopPropagation();
                  handleOpenTagEdit(record);
                }}
                style={{
                  padding: 0,
                  minWidth: "auto",
                  height: "auto",
                  flexShrink: 0,
                }}
              />
            )}
          </div>
        );
      },
    },
    {
      title: t("knowledge.directory"),
      dataIndex: "rel_path",
      width: 120,
      render: (rel_path: string) => {
        const kbName = detail.display_name || "-";
        if (!rel_path?.length) {
          return kbName;
        }

        const parts = rel_path.split("/").filter(Boolean);
        if (parts.length >= 2) {
          return `${kbName}/${parts[0]}`;
        }
        return kbName;
      },
    },
    {
      title: t("knowledge.parseStatus"),
      dataIndex: "document_stage",
      width: 120,
      render: (document_stage: string) => {
        const text =
          (DocumentStageEnum[document_stage as keyof typeof DocumentStageEnum]
            ? t(
                DocumentStageEnum[
                  document_stage as keyof typeof DocumentStageEnum
                ],
              )
            : document_stage) ||
          "-";
        const color =
          DocumentStageTagColorMap[
            document_stage as keyof typeof DocumentStageTagColorMap
          ] || "default";

        return <Tag color={color}>{text}</Tag>;
      },
    },
    {
      title: t("knowledge.docType"),
      dataIndex: "type",
      width: 120,
      render: (type: string, record: TreeNode) => {
        if (type === DocTypeEnum.Folder) {
          return t("knowledge.folder");
        }
        return FileUtils.getSuffix(record.display_name || "") || t("knowledge.unknown");
      },
    },
    {
      title: t("knowledge.size"),
      dataIndex: "document_size",
      width: 120,
      render: (_: number, record: TreeNode) => {
        return FileUtils.formatFileSize(record.document_size);
      },
    },
    {
      title: t("knowledge.updateDate"),
      dataIndex: "update_time",
      width: 180,
      render: (text: string) => moment(text).format(TIME_FORMAT),
    },
    {
      title: t("knowledge.updater"),
      dataIndex: "creator",
      width: 120,
    },
    {
      title: t("common.actions"),
      key: "action",
      width: 140,
      fixed: "right",
      render: (record: TreeNode) => {
        const canDownload =
          hasWritePermission || hasOnlyReadPermission || hasUploadPermission;
        if (!canDownload) return null;

        const downloadBtn = (
          <Button type="link" size="small" onClick={() => onDownload(record)}>
            {t("knowledge.download")}
          </Button>
        );
        const importBtn = (
          <Button
            type="link"
            size="small"
            onClick={() => {
              const parents = TreeUtils.findParents(
                tableData,
                record.document_id || "",
              );
              onImportKnowledge({
                targetPath: parents.map((item) => item.display_name).join("/"),
                p_id: record.document_id,
              });
            }}
          >
            {t("knowledge.importFile")}
          </Button>
        );

        if (!hasWritePermission) {
          if (record.isLeaf) return downloadBtn;
          return hasUploadPermission ? importBtn : null;
        }

        const isParsePending =
          record.document_stage === DocDocumentStageEnum.DocumentParsing;
        const isUnParse =
          record.document_stage === DocDocumentStageEnum.DocumentUploaded;

        const defaultItems: MenuProps["items"] = [
          {
            key: "rename",
            label: t("common.edit"),
          },
          {
            key: "move",
            label: t("knowledge.moveTo"),
            disabled: !detail?.acl?.includes("DATASET_WRITE"),
          },
        ];

        const isLeftItems: MenuProps["items"] = [...defaultItems];
        if (!isParsePending) {
          if (isUnParse) {
            isLeftItems.push({
              key: "parse",
              label: t("knowledge.parse"),
            });
          } else {
            isLeftItems.push({
              key: "reparse",
              label: t("knowledge.reparse"),
            });
          }
        }
        isLeftItems.push({ key: "delete", label: t("common.delete"), danger: true });
        const notLeafItems: MenuProps["items"] = [
          {
            key: "rename",
            label: t("common.edit"),
          },
          { key: "delete", label: t("common.delete"), danger: true },
        ];
        return (
          <div>
            {record.isLeaf ? downloadBtn : importBtn}
            <Dropdown
              menu={{
                items: record.isLeaf ? isLeftItems : notLeafItems,
                onClick: (e) => {
                  e.domEvent.preventDefault();
                  handleMenuClick(e, record);
                },
              }}
            >
              <Button
                type="link"
                size="small"
                icon={<DownOutlined />}
                iconPosition="end"
              >
                {t("knowledge.more")}
              </Button>
            </Dropdown>
          </div>
        );
      },
    },
  ];

  const tableDataRefresh = () => {
    setTimeout(() => {
      getDocumentData({
        pId: "",
        level: 0,
        page: pagination.current,
        pageSize: pagination.pageSize,
      });
    }, 300);
  };

  const resetSelectionState = () => {
    setSelectedRowKeys([]);
  };

  const handleMenuClick = (e: { key: string }, record: TreeNode) => {
    if (!e.key) {
      return;
    }
    if (e.key === "delete") {
      handleDelete([record]);
      return;
    }
    setCurrentNode(record);
    switch (e.key) {
      case "rename": {
        const suffix =
          record.data_source_type === DocDataSourceTypeEnum.LocalFile
            ? FileUtils.getSuffix(record.display_name || "", true)
            : "";
        const reg = new RegExp(suffix.replace(/\./g, "\\.") + "$", "i");
        const nameMaxLen = !record.isLeaf
          ? 30
          : Math.max(300 - suffix.length, 1);
        knowledgeRenameRef.current?.onOpen({
          title: t("common.edit"),
          form: {
            name: !record.isLeaf
              ? t("knowledge.editFolderName")
              : t("knowledge.editKnowledgeName"),
            namePlaceholder: !record.isLeaf
              ? t("knowledge.folderNameRule")
              : t("knowledge.inputKnowledgeName"),
            nameLen: nameMaxLen,
            nameRules: [
              {
                required: true,
                validator: (_: unknown, value: string): Promise<void> => {
                  if (!value) {
                    return Promise.reject(
                      !record.isLeaf
                        ? t("knowledge.inputFolderName")
                        : t("knowledge.inputKnowledgeName"),
                    );
                  }
                  if (!record.isLeaf) {
                    if (!FOLDER_NAME_REG.test(value) || value.length > 30) {
                      return Promise.reject(
                        t("knowledge.folderNameRule"),
                      );
                    }
                  } else {
                    if (value.length + suffix.length > 300) {
                      return Promise.reject(t("knowledge.maxLength300"));
                    }
                  }
                  return Promise.resolve();
                },
              },
            ],
            nameAdd: suffix || undefined,
          },
          data: {
            name: record.display_name?.replace(reg, "") || "",
            tags: record.tags,
          },
        });
        break;
      }
      case "download": {
        onDownload(record);
        break;
      }
      case "parse":
        JobServiceApi()
          .jobServiceCreateJob({
            dataset: record?.dataset_id || "",
            job: {
              document_ids: [record?.document_id || ""].filter((i) => !!i),
              job_type: JobJobTypeEnum.JobTypeParseUploaded,
            } as unknown as Job,
          })
          .then(() => {
            message.success(t("knowledge.parseTaskCreated"));
          })
          .catch((error) => {
            console.log(error);
          })
          .finally(() => {
            setCurrentNode(null);
            getImportingTotal();
            tableDataRefresh();
          });
        break;
      case "reparse":
        restartKnowledgeRef.current?.onOpen({
          title: t("knowledge.reparse"),
          dataset: record?.dataset_id || "",
          ids: [record?.document_id || ""],
        });
        break;
      case "import": {
        const parents = TreeUtils.findParents(
          tableData,
          record.document_id || "",
        );
        onImportKnowledge({
          targetPath: parents.map((item) => item.display_name).join("/"),
          p_id: record.document_id,
        });
        break;
      }
      case "move": {
        setAction("move");
        setShowCopyModal(true);
        setCurrentDocInfo(record);
        break;
      }
      default:
        break;
    }
  };

  const getDocumentData = async (params: {
    pId: string;
    level: number;
    parentNode?: TreeNode;
    page?: number;
    pageSize?: number;
  }) => {
    const {
      pId,
      level,
      parentNode,
      page = 1,
      pageSize: customPageSize,
    } = params;

    try {
      const isRootLevel = !pId && !parentNode;
      const currentPageSize = customPageSize || pagination.pageSize || 10;

      const searchParams: {
        parent: string;
        p_id: string;
        keyword: string;
        page_size: number;
        page_token?: string;
      } = {
        parent: "",
        p_id: pId,
        keyword,
        page_size: isRootLevel ? currentPageSize : 10000,
      };

      if (isRootLevel && page) {
        const updatedPagination = {
          ...pagination,
          current: page,
          pageSize: currentPageSize,
        };
        setPagination(updatedPagination);

        searchParams.page_token = UIUtils.generatePageToken({
          page: page - 1,
          pageSize: currentPageSize,
          total: pagination.total || 0,
        });
      }

      const res = await DocumentServiceApi().documentServiceSearchDocuments({
        dataset: detail.dataset_id!,
        searchDocumentsRequest: searchParams,
      });

      const documents = res.data.documents.map((doc: Doc) => ({
        ...doc,
        level: level,
        isLeaf: doc.type !== DocTypeEnum.Folder,
        loaded: false,
      }));
      if (parentNode) {
        setTableData((prevData) => {
          const newData = cloneDeep(prevData);
          const updateNode = (nodes: TreeNode[]): TreeNode[] => {
            return nodes.map((node) => {
              if (node.document_id === parentNode.document_id) {
                return {
                  ...node,
                  children: documents as TreeNode[],
                  loaded: true,
                };
              }
              if (node.children) {
                return { ...node, children: updateNode(node.children) };
              }
              return node;
            });
          };
          return updateNode(newData);
        });
        if (
          parentNode.document_id &&
          selectedRowKeys.includes(parentNode.document_id)
        ) {
          const childKeys = documents
            .map((doc) => doc.document_id)
            .filter((id): id is string => Boolean(id));
          setSelectedRowKeys((prev) =>
            Array.from(new Set([...prev, ...childKeys])),
          );
        }
      } else {
        if (isRootLevel) {
          setExpandedRowKeys([]);
        }
        setTableData(documents as TreeNode[]);
        setExpandedRowKeys([]);
        if (isRootLevel && res.data.total_size !== undefined) {
          setPagination((prev) => ({ ...prev, total: res.data.total_size }));
        }
      }
    } catch (error) {
      console.error("Failed to load documents:", error);
    }
  };

  const downloadCheckedKnowledge = () => {
    if (selectedRowKeys.length === 0) {
      return;
    }
    const records = selectedRowKeys.map((key) =>
      TreeUtils.findNode(tableData, (node: TreeNode) => {
        return node.document_id === key;
      }),
    );
    if (records.length === 0) {
      return;
    }
    records.forEach((record) => onDownload(record));
  };

  const restartCheckedKnowledge = () => {
    if (selectedRowKeys.length === 0) {
      message.warning(t("knowledge.selectAtLeastOneFile"));
      return;
    }
    const records = selectedRowKeys.map((key) =>
      TreeUtils.findNode(
        tableData,
        (node: TreeNode) => node.document_id === key,
      ),
    );
    if (records.length === 0) {
      message.warning(t("knowledge.selectAtLeastOneFile"));
      return;
    }
    restartKnowledgeRef.current?.onOpen({
      title: t("knowledge.reparse"),
      dataset: detail.dataset_id!,
      ids: records.map((record) => record.document_id || ""),
    });
  };

  const updateDocument = (params?: {
    documentId: string;
    level?: number;
    parentNode?: TreeNode;
  }) => {
    if (!params?.documentId) {
      return;
    }
    getDocumentData({
      pId: params.documentId,
      level: params.level || 0,
      parentNode: params.parentNode,
    });
  };

  const onTableChange = (newPagination: TablePaginationConfig) => {
    setPagination({
      current: newPagination.current,
      pageSize: newPagination.pageSize,
      total: pagination.total,
    });

    getDocumentData({
      pId: "",
      level: 0,
      page: newPagination.current,
      pageSize: newPagination.pageSize,
    });
  };

  useImperativeHandle(ref, () => ({
    getTableData: (params) => {
      getDocumentData({
        pId: params?.pId || "",
        level: params?.level || 0,
        parentNode: params?.parentNode,
      });
    },
    treeData: tableData,
    deleteKnowledge: () => {
      if (selectedRowKeys.length === 0) {
        message.warning(t("knowledge.selectAtLeastOneFile"));
        return;
      }
      handleDelete(
        selectedRowKeys.map((key) =>
          TreeUtils.findNode(
            tableData,
            (node: TreeNode) => node.document_id === key,
          ),
        ),
      );
    },
    downloadCheckedKnowledge,
    updateDocument,
    restartCheckedKnowledge: restartCheckedKnowledge,
    openBatchEditTags: () => {
      void doOpenBatchEditTags();
    },
    openBatchMove: () => {
      void doOpenBatchMove();
    },
    refresh: (value: string) => {
      setKeyword(value);
    },
  }));

  useEffect(() => {
    if (detail.dataset_id) {
      setPagination({ current: 1, pageSize: 10, total: 0 });
      getDocumentData({ pId: "", level: 0, page: 1 });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail.dataset_id]);

  useEffect(() => {
    setPagination((prev) => ({ ...prev, current: 1 }));
    getDocumentData({ pId: "", level: 0, page: 1 });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [keyword]);

  return (
    <div style={{ width: "100%", minWidth: 0 }}>
      <Table<TreeNode>
        columns={columns as ColumnType<TreeNode>[]}
        dataSource={tableData}
        pagination={getLocalizedTablePagination({
          ...pagination,
          showSizeChanger: true,
          showTotal: (total: number) => t("knowledge.totalCount", { total }),
        }, t)}
        onChange={onTableChange}
        rowKey="document_id"
        scroll={{ x: 1600, y: "calc(100vh - 380px)" }}
        expandable={{
          expandedRowKeys,
          onExpand: (expanded, record) => handleExpand(expanded, record),
          expandIcon: () => null,
        }}
      />
      <RenameModel ref={knowledgeRenameRef} onSubmit={onRename} />
      <RestartKnowledgeModal
        ref={restartKnowledgeRef}
        onFinish={() => {
          setCurrentNode(null);
          getImportingTotal();
          tableDataRefresh();
        }}
        parsers={detail.parsers}
      />
      {showCopyModal && (
        <CopyMoveModal
          cancelFn={() => setShowCopyModal(false)}
          currentData={currentDocInfo as TreeNode}
          action={action}
          onSuccess={() => {
            resetSelectionState();
            setTimeout(() => {
              getImportingTotal();
              tableDataRefresh();
            }, 3000);
          }}
        />
      )}
      {}
      <EditTags
        open={showTagEditModal}
        record={tagEditRecord}
        datasetId={detail.dataset_id || ""}
        onCancel={handleCloseTagEdit}
        onSuccess={handleTagEditSuccess}
      />
      {}
      <BatchEditTags
        open={batchTagEditState.showModal}
        selectedFileCount={batchTagEditState.selectedFileCount}
        documentIds={batchTagEditState.documentIds}
        folderIds={batchTagEditState.folderIds}
        datasetId={detail.dataset_id || ""}
        onCancel={() => {
          setBatchTagEditState({
            showModal: false,
            documentIds: [],
            folderIds: [],
            selectedFileCount: 0,
          });
        }}
        onSuccess={() => {
          resetSelectionState();
          tableDataRefresh();
        }}
      />
      <BatchMoveModal
        open={batchMoveState.showModal}
        datasetId={detail.dataset_id || ""}
        selectedFileCount={batchMoveState.selectedFileCount}
        documents={batchMoveState.documents}
        onCancel={() => {
          setBatchMoveState({
            showModal: false,
            documents: [],
            selectedFileCount: 0,
          });
        }}
        onSuccess={() => {
          resetSelectionState();
          getImportingTotal();
          tableDataRefresh();
        }}
      />
    </div>
  );
});

KnowledgeTable.displayName = "KnowledgeTable";

export default KnowledgeTable;
