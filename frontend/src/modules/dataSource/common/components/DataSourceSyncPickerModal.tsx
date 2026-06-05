import { useEffect, useMemo, useState, type Key } from "react";
import { Alert, Button, Empty, Input, Modal, Space, Tree } from "antd";
import type { DataNode } from "antd/es/tree";
import type { TreeProps } from "antd";
import { SearchOutlined } from "@ant-design/icons";

type LazySyncDataNode = DataNode & {
  childrenLoaded?: boolean;
};

export interface DataSourceSyncPickerModalProps {
  t: any;
  open: boolean;
  syncSubmitting: boolean;
  selectedCount: number;
  syncKeyword: string;
  setSyncKeyword: (value: string) => void;
  hasFilteredSelected: boolean;
  filteredSyncNodeKeys: string[];
  setSyncSelectedDocIds: (updater: string[] | ((prev: string[]) => string[])) => void;
  syncTreeLoading: boolean;
  syncTreeData: DataNode[];
  checkedTreeKeys: TreeProps["checkedKeys"];
  selectableSyncFileKeys: Set<string>;
  onLoadSyncTreeNode?: TreeProps["loadData"];
  onCancel: () => void;
  onOk: () => void;
}

export default function DataSourceSyncPickerModal({
  t,
  open,
  syncSubmitting,
  selectedCount,
  syncKeyword,
  setSyncKeyword,
  hasFilteredSelected,
  filteredSyncNodeKeys,
  setSyncSelectedDocIds,
  syncTreeLoading,
  syncTreeData,
  checkedTreeKeys,
  selectableSyncFileKeys,
  onLoadSyncTreeNode,
  onCancel,
  onOk,
}: DataSourceSyncPickerModalProps) {
  const [expandedKeys, setExpandedKeys] = useState<Key[]>([]);
  const treeKeySet = useMemo(() => {
    const keys = new Set<Key>();
    const collectKeys = (nodes: DataNode[]) => {
      nodes.forEach((node) => {
        keys.add(node.key);
        if (node.children) {
          collectKeys(node.children);
        }
      });
    };
    collectKeys(syncTreeData);
    return keys;
  }, [syncTreeData]);

  useEffect(() => {
    if (!open) {
      setExpandedKeys([]);
    }
  }, [open]);

  useEffect(() => {
    setExpandedKeys((current) => current.filter((key) => treeKeySet.has(key)));
  }, [treeKeySet]);

  const isTreeNodeLoaded = (node: DataNode) =>
    Boolean((node as LazySyncDataNode).childrenLoaded);

  const loadTreeNode = (node: DataNode) => {
    if (node.isLeaf || isTreeNodeLoaded(node)) {
      return;
    }
    void onLoadSyncTreeNode?.(node as Parameters<NonNullable<TreeProps["loadData"]>>[0]);
  };

  const toggleTreeNode = (node: DataNode) => {
    if (node.isLeaf) {
      return;
    }

    if (!expandedKeys.includes(node.key)) {
      setExpandedKeys((current) =>
        current.includes(node.key) ? current : [...current, node.key],
      );
      loadTreeNode(node);
      return;
    }

    if (!isTreeNodeLoaded(node)) {
      loadTreeNode(node);
      return;
    }

    setExpandedKeys((current) => current.filter((key) => key !== node.key));
  };

  return (
    <Modal
      title={t("admin.dataSourceDetailManualPullTitle")}
      open={open}
      onCancel={onCancel}
      okText={t("admin.dataSourceDetailStartPull", { count: selectedCount })}
      okButtonProps={{ disabled: selectedCount === 0 || syncSubmitting, loading: syncSubmitting }}
      onOk={onOk}
      width={860}
      destroyOnClose
    >
      <div className="data-source-sync-picker">
        <Space wrap className="data-source-sync-picker-filters">
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder={t("admin.dataSourceDetailSearchInModalPlaceholder")}
            value={syncKeyword}
            onChange={(event) => setSyncKeyword(event.target.value)}
            className="data-source-sync-picker-keyword"
          />
          <Space wrap className="data-source-sync-picker-actions">
            {hasFilteredSelected ? (
              <Button
                onClick={() =>
                  setSyncSelectedDocIds((prev) =>
                    prev.filter((id) => !filteredSyncNodeKeys.includes(id)),
                  )
                }
                disabled={filteredSyncNodeKeys.length === 0}
              >
                {t("chat.cancelSelectAll")}
              </Button>
            ) : (
              <Button
                onClick={() => setSyncSelectedDocIds(filteredSyncNodeKeys)}
                disabled={filteredSyncNodeKeys.length === 0}
              >
                {t("chat.selectAll")}
              </Button>
            )}
          </Space>
        </Space>

        <Alert
          showIcon
          type="info"
          message={t("admin.dataSourceDetailTreeSelectTitle")}
          description={t("admin.dataSourceDetailTreeSelectDesc")}
        />

        {syncTreeLoading ? (
          <div className="data-source-sync-tree-loading">加载目录树中...</div>
        ) : syncTreeData.length > 0 ? (
          <Tree
            blockNode
            checkable
            checkStrictly
            checkedKeys={checkedTreeKeys}
            expandedKeys={expandedKeys}
            loadData={onLoadSyncTreeNode}
            treeData={syncTreeData}
            className="data-source-sync-tree"
            onExpand={(keys, info) => {
              setExpandedKeys(keys);
              if (info.expanded) {
                loadTreeNode(info.node);
              }
            }}
            onSelect={(_keys, info) => {
              toggleTreeNode(info.node);
            }}
            onCheck={(keys) => {
              const nextKeys = Array.isArray(keys) ? keys : keys.checked;
              setSyncSelectedDocIds(
                nextKeys
                  .map((key) => `${key}`)
                  .filter((key) => selectableSyncFileKeys.has(key)),
              );
            }}
          />
        ) : (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={t("admin.dataSourceDetailNoMatchedFile")}
          />
        )}
      </div>
    </Modal>
  );
}
