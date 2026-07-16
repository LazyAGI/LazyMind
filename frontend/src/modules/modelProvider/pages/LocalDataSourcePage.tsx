import { useMemo } from "react";
import { Alert, Button, Empty, Modal, Spin, Tree, Typography } from "antd";
import type { DataNode } from "antd/es/tree";
import {
  ArrowLeftOutlined,
  FolderOpenOutlined,
  FolderOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import { useNavigate } from "react-router-dom";
import { useLocalDataSourceSettings } from "../hooks/useLocalDataSourceSettings";
import { CLOUD_DOCUMENTS_PATH } from "../utils/cloudDocumentUrls";

const { Paragraph, Text } = Typography;

interface DirectoryTrieNode {
  children: Map<string, DirectoryTrieNode>;
  fullPath: string;
  segment: string;
  sourceNames: Set<string>;
}

export default function LocalDataSourcePage() {
  const navigate = useNavigate();
  const {
    t,
    loading,
    canCreateLocalSource,
    localSourceCount,
    localChatSources,
    chatSettingsLoading,
    chatSettingsLoadFailed,
    chatSettingsSaving,
    chatSettingsModalOpen,
    selectedBindingIds,
    setSelectedBindingIds,
    setChatSettingsModalOpen,
    handleOpenChatSettings,
    handleRetryChatSettings,
    handleSaveChatSettings,
  } = useLocalDataSourceSettings();
  const bindingIdSet = useMemo(
    () =>
      new Set(
        localChatSources.flatMap((source) =>
          source.bindings.map((binding) => binding.binding_id),
        ),
      ),
    [localChatSources],
  );
  const treeData = useMemo<DataNode[]>(
    () =>
      localChatSources.map((source) => ({
        key: `source:${source.source_id}`,
        title: source.name,
        selectable: false,
        children: source.bindings.map((binding) => ({
          key: binding.binding_id,
          title: binding.target_ref,
          isLeaf: true,
        })),
      })),
    [localChatSources],
  );
  const enabledDirectories = useMemo(
    () =>
      localChatSources.flatMap((source) =>
        source.bindings
          .filter((binding) => binding.chat_enabled)
          .map((binding) => ({
            path: binding.target_ref,
            sourceName: source.name,
          })),
      ),
    [localChatSources],
  );
  const directoryTreeData = useMemo<DataNode[]>(() => {
    const root: DirectoryTrieNode = {
      children: new Map(),
      fullPath: "",
      segment: "",
      sourceNames: new Set(),
    };

    enabledDirectories.forEach((directory) => {
      const normalizedPath = directory.path.trim().replace(/\\/g, "/");
      const segments = normalizedPath.split("/").filter(Boolean);
      const absolute = normalizedPath.startsWith("/");
      let current = root;
      let currentPath = absolute ? "/" : "";

      segments.forEach((segment) => {
        currentPath = currentPath === "/"
          ? `/${segment}`
          : currentPath
            ? `${currentPath}/${segment}`
            : segment;
        let child = current.children.get(segment);
        if (!child) {
          child = {
            children: new Map(),
            fullPath: currentPath,
            segment,
            sourceNames: new Set(),
          };
          current.children.set(segment, child);
        }
        current = child;
      });
      current.sourceNames.add(directory.sourceName);
    });

    const toTreeNode = (node: DirectoryTrieNode, topLevel: boolean): DataNode => {
      let collapsedNode = node;
      const collapsedSegments = [node.segment];

      while (
        collapsedNode.sourceNames.size === 0 &&
        collapsedNode.children.size === 1
      ) {
        collapsedNode = Array.from(collapsedNode.children.values())[0];
        collapsedSegments.push(collapsedNode.segment);
      }

      const sourceNames = Array.from(collapsedNode.sourceNames);
      const label = topLevel
        ? collapsedNode.fullPath
        : collapsedSegments.join("/");

      return {
        key: collapsedNode.fullPath,
        icon: <FolderOutlined />,
        title: (
          <span className="model-provider-local-chat-tree-title">
            <span className="model-provider-local-chat-tree-label" title={collapsedNode.fullPath}>
              {label}
            </span>
            {sourceNames.length > 0 && (
              <span className="model-provider-local-chat-tree-sources">
                {t("modelProvider.cloudDocuments.localChatDirectorySources", {
                  names: sourceNames.join(", "),
                })}
              </span>
            )}
          </span>
        ),
        children: Array.from(collapsedNode.children.values()).map((child) =>
          toTreeNode(child, false),
        ),
      };
    };

    return Array.from(root.children.values()).map((node) =>
      toTreeNode(node, true),
    );
  }, [enabledDirectories, t]);

  return (
    <div className="model-provider-page-content model-provider-service-page model-provider-cloud-doc-local-page">
      <button
        type="button"
        className="model-provider-cloud-doc-breadcrumb"
        onClick={() => navigate(CLOUD_DOCUMENTS_PATH)}
      >
        <ArrowLeftOutlined />
        <span>{t("modelProvider.cloudDocuments.backToProviders")}</span>
      </button>

      <Spin spinning={loading}>
        <section className="model-provider-service-category model-provider-cloud-doc-local-section">
          <div className="model-provider-service-category-top">
            <div className="model-provider-service-category-head">
              <span className="model-provider-cloud-doc-local-logo">
                <FolderOpenOutlined />
              </span>
              <div>
                <h3>{t("modelProvider.cloudDocuments.localDetailTitle")}</h3>
                <p>{t("modelProvider.cloudDocuments.localDetailSubtitle")}</p>
              </div>
            </div>
            <div
              className="model-provider-cloud-doc-local-summary"
              aria-label={`${t("modelProvider.cloudDocuments.localConnectedCountLabel")}: ${localSourceCount}`}
            >
              <strong>{localSourceCount}</strong>
              <Text type="secondary">
                {t("modelProvider.cloudDocuments.localConnectedCountLabel")}
              </Text>
            </div>
          </div>

          <div className="model-provider-cloud-doc-setting-card is-directory-config">
            <div className="model-provider-cloud-doc-setting-head">
              <div className="model-provider-cloud-doc-setting-copy">
                <h4>{t("modelProvider.cloudDocuments.localChatDirectoriesTitle")}</h4>
                <Paragraph>{t("modelProvider.cloudDocuments.localChatDirectoriesHint")}</Paragraph>
              </div>
              <Button
                icon={<SettingOutlined />}
                loading={chatSettingsLoading && !chatSettingsModalOpen}
                onClick={() => void handleOpenChatSettings()}
              >
                {t("modelProvider.cloudDocuments.localChatDirectoriesConfigure")}
              </Button>
            </div>

            {chatSettingsLoadFailed ? (
              <Alert
                showIcon
                type="error"
                message={t("modelProvider.cloudDocuments.localChatDirectoriesLoadFailed")}
                action={
                  <Button size="small" onClick={() => void handleRetryChatSettings()}>
                    {t("common.retry")}
                  </Button>
                }
              />
            ) : directoryTreeData.length > 0 ? (
              <div
                className="model-provider-local-chat-directory-overview"
                role="region"
                aria-label={t("modelProvider.cloudDocuments.localChatDirectoriesTreeAria")}
              >
                <Tree
                  blockNode
                  defaultExpandAll
                  selectable={false}
                  showIcon
                  showLine={{ showLeafIcon: false }}
                  treeData={directoryTreeData}
                />
              </div>
            ) : (
              <Text type="secondary" className="model-provider-local-chat-empty-text">
                {t("modelProvider.cloudDocuments.localChatDirectoriesEmpty")}
              </Text>
            )}
          </div>
        </section>
      </Spin>

      <Modal
        open={chatSettingsModalOpen}
        title={t("modelProvider.cloudDocuments.localChatDirectoriesModalTitle")}
        okText={t("common.save")}
        cancelText={t("common.cancel")}
        confirmLoading={chatSettingsSaving}
        okButtonProps={{
          disabled:
            !canCreateLocalSource ||
            chatSettingsLoading ||
            chatSettingsLoadFailed,
        }}
        cancelButtonProps={{ disabled: chatSettingsSaving }}
        maskClosable={!chatSettingsSaving}
        onCancel={() => {
          if (!chatSettingsSaving) setChatSettingsModalOpen(false);
        }}
        onOk={() => void handleSaveChatSettings()}
        width={680}
        destroyOnClose
      >
        <div className="model-provider-local-chat-directory-modal">
          <Alert
            showIcon
            type="info"
            message={t("modelProvider.cloudDocuments.localChatDirectoriesModalHint")}
          />

          {chatSettingsLoading ? (
            <div className="model-provider-local-chat-tree-state">
              <Spin size="small" />
              <span>{t("common.loading")}</span>
            </div>
          ) : chatSettingsLoadFailed ? (
            <Alert
              showIcon
              type="error"
              message={t("modelProvider.cloudDocuments.localChatDirectoriesLoadFailed")}
              action={
                <Button size="small" onClick={() => void handleRetryChatSettings()}>
                  {t("common.retry")}
                </Button>
              }
            />
          ) : treeData.length > 0 ? (
            <div
              className="model-provider-local-chat-tree"
              role="region"
              aria-label={t("modelProvider.cloudDocuments.localChatDirectoriesTreeAria")}
            >
              <Tree
                blockNode
                checkable
                defaultExpandAll
                checkedKeys={selectedBindingIds}
                treeData={treeData}
                onCheck={(keys) => {
                  const checkedKeys = Array.isArray(keys) ? keys : keys.checked;
                  setSelectedBindingIds(
                    checkedKeys
                      .map((key) => `${key}`)
                      .filter((key) => bindingIdSet.has(key)),
                  );
                }}
              />
            </div>
          ) : (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={t("modelProvider.cloudDocuments.localChatDirectoriesNoBindings")}
            >
              <Button onClick={() => navigate("/data-sources")}>
                {t("modelProvider.cloudDocuments.localManageDataSources")}
              </Button>
            </Empty>
          )}
        </div>
      </Modal>
    </div>
  );
}
