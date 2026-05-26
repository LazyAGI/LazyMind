import { Alert, Button, Empty, Input, Modal, Space, Tree } from "antd";
import type { DataNode } from "antd/es/tree";
import { SearchOutlined } from "@ant-design/icons";

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
  checkedTreeKeys: string[];
  selectableSyncFileKeys: Set<string>;
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
  onCancel,
  onOk,
}: DataSourceSyncPickerModalProps) {
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
            checkable
            defaultExpandAll
            checkedKeys={checkedTreeKeys}
            treeData={syncTreeData}
            className="data-source-sync-tree"
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
