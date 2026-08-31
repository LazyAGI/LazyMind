import { useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import {
  Button,
  Checkbox,
  Empty,
  Form,
  Input,
  Radio,
  Select,
  Spin,
  Tag,
  Tooltip,
  TreeSelect,
  Typography,
} from "antd";
import type { FormInstance, TreeSelectProps } from "antd";
import type { CheckboxChangeEvent } from "antd/es/checkbox";
import type { DataNode } from "antd/es/tree";
import {
  CheckCircleFilled,
  FolderAddOutlined,
  FolderOpenOutlined,
  ReloadOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import type { TFunction } from "i18next";
import {
  KNOWLEDGE_BASE_NAME_MAX_LENGTH,
  KNOWLEDGE_BASE_NAME_PATTERN,
} from "@/modules/knowledge/constants/validation";
import { DATA_SOURCE_FILE_TYPE_OPTIONS } from "../../constants/options";
import type {
  SourceFormValues,
  SourceType,
  SyncMode,
} from "../../constants/types";
import {
  buildTreeValuePathMap,
  buildTreeValueTitleMap,
  collapseSelectedTreeValues,
  collectTreeExpandableKeys,
  getTreeSelectLabelText,
  normalizeTreeSelectValues,
  type CollapsibleTreeNode,
  type LocalPathSelectOption,
} from "./treeSelectUtils";
import {
  getFeishuTargetDisplayText,
  getFeishuTargetValuePath,
} from "./feishuTargetUtils";
import type { LocalPathRecommendation } from "../../utils/feishuTarget";
import type { DesktopLocalFolderAccessState } from "@/runtime/desktopBridge";
import WizardSchedulePanel from "./WizardSchedulePanel";

const { Text } = Typography;

export interface WizardConnectionStepProps {
  t: TFunction;
  form: FormInstance<SourceFormValues>;
  selectedType: SourceType;
  syncMode: SyncMode;
  localPathOptions: LocalPathSelectOption[];
  localPathLoading: boolean;
  localPathRecommendations: LocalPathRecommendation[];
  localPathRecommendationsLoading: boolean;
  localPathRecommendationsError: string;
  localDiscoveryAccess?: DesktopLocalFolderAccessState | null;
  localDiscoveryChoosing?: boolean;
  feishuTargetLoading: boolean;
  feishuTargetTreeData: DataNode[];
  onLoadLocalPathOptions?: (path?: string) => void;
  onSearchLocalPathOptions?: (keyword: string) => void;
  onLoadLocalPathChildren?: TreeSelectProps["loadData"];
  onResetLocalPathBrowseOptions?: () => void;
  onLoadLocalPathRecommendations?: () => void;
  onChooseLocalDiscoveryLocations?: () => void;
  onLoadFeishuTargetOptions?: () => void;
  onSearchFeishuTargetOptions?: (keyword: string) => void;
  onLoadFeishuTargetChildren?: TreeSelectProps["loadData"];
}

export default function WizardConnectionStep({
  t,
  form,
  selectedType,
  syncMode,
  localPathOptions,
  localPathLoading,
  localPathRecommendations,
  localPathRecommendationsLoading,
  localPathRecommendationsError,
  localDiscoveryAccess = null,
  localDiscoveryChoosing = false,
  feishuTargetLoading,
  feishuTargetTreeData,
  onLoadLocalPathOptions,
  onSearchLocalPathOptions,
  onLoadLocalPathChildren,
  onResetLocalPathBrowseOptions,
  onLoadLocalPathRecommendations,
  onChooseLocalDiscoveryLocations,
  onLoadFeishuTargetOptions,
  onSearchFeishuTargetOptions,
  onLoadFeishuTargetChildren,
}: WizardConnectionStepProps) {
  const [localPathSearchValue, setLocalPathSearchValue] = useState("");
  const [localPathExpandedKeys, setLocalPathExpandedKeys] = useState<
    Array<string | number>
  >([]);
  const [localPathBrowseExpandedKeys, setLocalPathBrowseExpandedKeys] =
    useState<Array<string | number>>([]);
  const [feishuTargetSearchValue, setFeishuTargetSearchValue] = useState("");
  const [feishuTargetExpandedKeys, setFeishuTargetExpandedKeys] = useState<
    Array<string | number>
  >([]);
  const [feishuTargetBrowseExpandedKeys, setFeishuTargetBrowseExpandedKeys] =
    useState<Array<string | number>>([]);
  const [localPathBrowseKey, setLocalPathBrowseKey] = useState(0);
  const [feishuTargetTitleCache, setFeishuTargetTitleCache] = useState(
    () => new Map<string, string>(),
  );
  const [feishuTargetPathCache, setFeishuTargetPathCache] = useState(
    () => new Map<string, string>(),
  );

  const isLocalPathSearching = Boolean(localPathSearchValue.trim());
  const isFeishuTargetSearching = Boolean(feishuTargetSearchValue.trim());
  const canChooseLocalDiscoveryLocations = localDiscoveryAccess?.available === true;
  const hasLocalDiscoveryLocations = Boolean(
    localDiscoveryAccess?.discoveryConsentGranted &&
      localDiscoveryAccess.discoveryRoots.length > 0,
  );

  useEffect(() => {
    if (!isLocalPathSearching) {
      setLocalPathExpandedKeys(localPathBrowseExpandedKeys);
      return;
    }
    if (localPathLoading) {
      return;
    }
    setLocalPathExpandedKeys(
      collectTreeExpandableKeys(localPathOptions as CollapsibleTreeNode[]),
    );
  }, [
    localPathOptions,
    localPathLoading,
    isLocalPathSearching,
    localPathBrowseExpandedKeys,
  ]);

  useEffect(() => {
    if (!isFeishuTargetSearching) {
      setFeishuTargetExpandedKeys(feishuTargetBrowseExpandedKeys);
      return;
    }
    if (feishuTargetLoading) {
      return;
    }
    setFeishuTargetExpandedKeys(
      collectTreeExpandableKeys(feishuTargetTreeData as CollapsibleTreeNode[]),
    );
  }, [
    feishuTargetTreeData,
    feishuTargetLoading,
    isFeishuTargetSearching,
    feishuTargetBrowseExpandedKeys,
  ]);

  useEffect(() => {
    if (feishuTargetTreeData.length === 0) {
      return;
    }
    const nextTitles = buildTreeValueTitleMap(
      feishuTargetTreeData as CollapsibleTreeNode[],
    );
    const nextPaths = buildTreeValuePathMap(
      feishuTargetTreeData as CollapsibleTreeNode[],
    );
    if (nextTitles.size === 0 && nextPaths.size === 0) {
      return;
    }
    setFeishuTargetTitleCache((prev) => {
      const merged = new Map(prev);
      nextTitles.forEach((title, value) => {
        if (title && title !== value) {
          merged.set(value, title);
        }
      });
      return merged;
    });
    setFeishuTargetPathCache((prev) => {
      const merged = new Map(prev);
      nextPaths.forEach((path, value) => {
        if (path && path !== value) {
          merged.set(value, path);
        }
      });
      return merged;
    });
  }, [feishuTargetTreeData]);

  const localPathValue = Form.useWatch("path", form);
  const selectedLocalPathValues = normalizeTreeSelectValues(localPathValue);
  const selectedLocalPathValueSet = new Set(selectedLocalPathValues);
  const selectedRecommendationCount = localPathRecommendations.filter((item) =>
    selectedLocalPathValueSet.has(item.value),
  ).length;
  const allRecommendationsSelected =
    localPathRecommendations.length > 0 &&
    selectedRecommendationCount === localPathRecommendations.length;

  const updateRecommendedPathSelection = (values: string[]) => {
    form.setFieldValue("path", values);
  };

  const toggleRecommendedPath = (value: string, checked: boolean) => {
    const nextValues = checked
      ? Array.from(new Set([...selectedLocalPathValues, value]))
      : selectedLocalPathValues.filter((item) => item !== value);
    updateRecommendedPathSelection(nextValues);
  };

  const toggleAllRecommendedPaths = () => {
    const recommendedValueSet = new Set(
      localPathRecommendations.map((item) => item.value),
    );
    const nextValues = allRecommendationsSelected
      ? selectedLocalPathValues.filter((item) => !recommendedValueSet.has(item))
      : Array.from(
          new Set([
            ...selectedLocalPathValues,
            ...localPathRecommendations.map((item) => item.value),
          ]),
        );
    updateRecommendedPathSelection(nextValues);
  };
  const selectedFeishuTargetValues = normalizeTreeSelectValues(
    Form.useWatch("target", form),
  );
  const feishuTargetTitle = selectedFeishuTargetValues
    .map(
      (value) =>
        feishuTargetPathCache.get(value) ||
        feishuTargetTitleCache.get(value) ||
        value,
    )
    .filter(Boolean)
    .join("\n");
  const fileTypeLabelMap = useMemo(
    () =>
      new Map(
        DATA_SOURCE_FILE_TYPE_OPTIONS.map((item) => [
          item.value,
          t(item.i18nKey),
        ]),
      ),
    [t],
  );

  const renderFileTypeMaxTagPlaceholder = (
    omittedValues: Array<{ value?: unknown; label?: ReactNode }>,
  ) => {
    if (omittedValues.length === 0) {
      return null;
    }

    const labels = omittedValues
      .map(
        (item) =>
          fileTypeLabelMap.get(`${item.value || ""}` as any) || item.label,
      )
      .filter(Boolean);

    return (
      <Tooltip
        title={
          <div className="data-source-tree-select-tooltip-list">
            {labels.map((label, index) => (
              <div key={`${getTreeSelectLabelText(label)}-${index}`}>
                {label}
              </div>
            ))}
          </div>
        }
      >
        <span>{`+ ${omittedValues.length} ...`}</span>
      </Tooltip>
    );
  };

  const renderFeishuTargetTag: TreeSelectProps["tagRender"] = ({
    label,
    value,
    closable,
    onClose,
  }) => (
    <Tooltip
      title={getFeishuTargetValuePath(
        value,
        label,
        feishuTargetPathCache,
        t,
        feishuTargetTitleCache,
      )}
    >
      <Tag
        className="data-source-tree-select-tag"
        closable={closable}
        onClose={onClose}
        onMouseDown={(event) => {
          event.preventDefault();
          event.stopPropagation();
        }}
      >
        <span className="data-source-tree-select-tag-label">
          {getFeishuTargetDisplayText(value, label, t, feishuTargetTitleCache)}
        </span>
      </Tag>
    </Tooltip>
  );

  const renderFeishuTargetMaxTagPlaceholder: TreeSelectProps["maxTagPlaceholder"] =
    (omittedValues) => {
      if (omittedValues.length === 0) {
        return null;
      }

      const paths = omittedValues
        .map((item) =>
          getFeishuTargetValuePath(
            item.value,
            item.label,
            feishuTargetPathCache,
            t,
            feishuTargetTitleCache,
          ),
        )
        .filter(Boolean);

      return (
        <Tooltip
          title={
            <div className="data-source-tree-select-tooltip-list">
              {paths.map((path, index) => (
                <div key={`${path}-${index}`}>{path}</div>
              ))}
            </div>
          }
        >
          <span>{`+ ${omittedValues.length} ...`}</span>
        </Tooltip>
      );
    };

  return (
    <div className="data-source-wizard-body">
      <section className="data-source-form-section">
        <div className="data-source-form-section-title">
          {t("admin.dataSourceBasicConfig")}
        </div>
        <Form.Item
          label={t("admin.dataSourceKnowledgeBaseName")}
          name="knowledgeBase"
          extra={t("knowledge.knowledgeNameRule")}
          rules={[
            {
              required: true,
              whitespace: true,
              message: t("admin.dataSourceKnowledgeBaseNameRequired"),
            },
            {
              pattern: KNOWLEDGE_BASE_NAME_PATTERN,
              message: t("knowledge.knowledgeNameRule"),
            },
          ]}
        >
          <Input
            maxLength={KNOWLEDGE_BASE_NAME_MAX_LENGTH}
            placeholder={t("admin.dataSourceKnowledgeBaseNamePlaceholder")}
          />
        </Form.Item>
      </section>

      <section className="data-source-form-section">
        <div className="data-source-form-section-title">
          {t("admin.dataSourceAccessConfig")}
        </div>
        {selectedType === "local" ? (
          <>
            <div className="data-source-local-recommendations">
              <div className="data-source-local-recommendations-header">
                <div className="data-source-local-recommendations-intro">
                  <span className="data-source-local-recommendations-icon">
                    <SearchOutlined />
                  </span>
                  <div className="data-source-local-recommendations-copy">
                    <div className="data-source-local-recommendations-heading">
                      <span className="data-source-local-recommendations-title">
                        {t("admin.dataSourceRecommendedPaths")}
                      </span>
                      {!localPathRecommendationsLoading ? (
                        <span className="data-source-local-recommendations-found">
                          {t("admin.dataSourceRecommendedPathsFound", {
                            total: localPathRecommendations.length,
                          })}
                        </span>
                      ) : null}
                    </div>
                    <Text
                      type="secondary"
                      className="data-source-local-recommendations-description"
                    >
                      {t("admin.dataSourceRecommendedPathsDescription")}
                    </Text>
                  </div>
                </div>
                <div className="data-source-local-recommendations-actions">
                  {selectedRecommendationCount > 0 ? (
                    <span className="data-source-local-recommendations-selected">
                      <CheckCircleFilled />
                      {t("admin.dataSourceRecommendedPathsSelected", {
                        selected: selectedRecommendationCount,
                      })}
                    </span>
                  ) : null}
                  {localPathRecommendations.length > 0 ? (
                    <Button
                      type="text"
                      size="small"
                      onClick={toggleAllRecommendedPaths}
                    >
                      {allRecommendationsSelected
                        ? t("common.cancelSelectAll")
                        : t("common.selectAll")}
                    </Button>
                  ) : null}
                  {canChooseLocalDiscoveryLocations ? (
                    <Tooltip
                      title={
                        hasLocalDiscoveryLocations
                          ? t("admin.dataSourceDiscoveryAddLocation")
                          : t("admin.dataSourceDiscoveryChooseLocation")
                      }
                    >
                      <Button
                        type="text"
                        size="small"
                        aria-label={t("admin.dataSourceDiscoveryChooseLocation")}
                        icon={<FolderAddOutlined />}
                        loading={localDiscoveryChoosing}
                        onClick={onChooseLocalDiscoveryLocations}
                      >
                        {hasLocalDiscoveryLocations
                          ? t("admin.dataSourceDiscoveryAddLocation")
                          : t("admin.dataSourceDiscoveryChooseLocation")}
                      </Button>
                    </Tooltip>
                  ) : null}
                  <Tooltip
                    title={t("admin.dataSourceRecommendedPathsRefresh")}
                  >
                    <Button
                      type="text"
                      shape="circle"
                      size="small"
                      aria-label={t("admin.dataSourceRecommendedPathsRefresh")}
                      icon={<ReloadOutlined />}
                      loading={localPathRecommendationsLoading}
                      onClick={onLoadLocalPathRecommendations}
                    />
                  </Tooltip>
                </div>
              </div>

              {localPathRecommendationsLoading ? (
                <div className="data-source-local-recommendations-state">
                  <Spin size="small" />
                  <Text type="secondary">
                    {t("admin.dataSourceRecommendedPathsLoading")}
                  </Text>
                </div>
              ) : localPathRecommendationsError ? (
                <Text type="danger">{localPathRecommendationsError}</Text>
              ) : localPathRecommendations.length === 0 ? (
                <Empty
                  className="data-source-local-recommendations-empty"
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={
                    hasLocalDiscoveryLocations
                      ? t("admin.dataSourceDiscoveryEmptyAuthorized", {
                          count: localDiscoveryAccess?.discoveryRoots.length || 0,
                        })
                      : canChooseLocalDiscoveryLocations
                        ? t("admin.dataSourceDiscoveryPermissionHint")
                        : t("admin.dataSourceRecommendedPathsEmpty")
                  }
                />
              ) : (
                <>
                  <div className="data-source-local-recommendations-list">
                    {localPathRecommendations.map((item) => (
                      <Checkbox
                        key={item.key}
                        className={
                          selectedLocalPathValueSet.has(item.value)
                            ? "is-selected"
                            : undefined
                        }
                        checked={selectedLocalPathValueSet.has(item.value)}
                        onChange={(event: CheckboxChangeEvent) =>
                          toggleRecommendedPath(item.value, event.target.checked)
                        }
                      >
                        <span className="data-source-local-recommendations-item">
                          <span className="data-source-local-recommendations-folder">
                            <FolderOpenOutlined />
                          </span>
                          <span className="data-source-local-recommendations-item-copy">
                            <span className="data-source-local-recommendations-name">
                              {item.title}
                            </span>
                            <span
                              className="data-source-local-recommendations-path"
                              title={item.path}
                            >
                              {item.path}
                            </span>
                          </span>
                        </span>
                      </Checkbox>
                    ))}
                  </div>
                  {localDiscoveryAccess?.truncated ? (
                    <Text type="secondary" className="data-source-local-discovery-note">
                      {t("admin.dataSourceDiscoveryScanLimited")}
                    </Text>
                  ) : null}
                </>
              )}
            </div>

            <Form.Item
              label={t("admin.dataSourceAccessPath")}
              name="path"
              getValueFromEvent={(value) =>
                collapseSelectedTreeValues(value, localPathOptions)
              }
              rules={[
                {
                  validator: (_rule, value) => {
                    const values = Array.isArray(value)
                      ? value
                      : value
                        ? [value]
                        : [];
                    return values.length > 0
                      ? Promise.resolve()
                      : Promise.reject(
                          new Error(t("admin.dataSourceAccessPathRequired")),
                        );
                  },
                },
              ]}
            >
              <TreeSelect
                key={`local-path-browse-${localPathBrowseKey}`}
                multiple
                allowClear
                filterTreeNode={false}
                loadData={onLoadLocalPathChildren}
                loading={localPathLoading}
                maxTagCount="responsive"
                notFoundContent={localPathLoading ? <Spin size="small" /> : null}
                placeholder="/mnt/team-share/ops-docs"
                searchValue={localPathSearchValue}
                showSearch
                style={{ width: "100%" }}
                treeCheckable
                treeData={localPathOptions}
                treeDefaultExpandAll={false}
                treeExpandedKeys={localPathExpandedKeys}
                treeLine
                showCheckedStrategy={TreeSelect.SHOW_PARENT}
                styles={{
                  popup: { root: { maxHeight: 360, overflow: "auto" } },
                }}
                onOpenChange={(open) => {
                  if (!open) {
                    setLocalPathSearchValue("");
                    setLocalPathExpandedKeys([]);
                    setLocalPathBrowseExpandedKeys([]);
                    setLocalPathBrowseKey((key) => key + 1);
                    onResetLocalPathBrowseOptions?.();
                    return;
                  }
                  onLoadLocalPathOptions?.(
                    selectedLocalPathValues.length === 1
                      ? selectedLocalPathValues[0]
                      : "",
                  );
                }}
                onSearch={(value) => {
                  setLocalPathSearchValue(value);
                  onSearchLocalPathOptions?.(value);
                }}
                onTreeExpand={(keys) => {
                  setLocalPathExpandedKeys(keys);
                  if (!isLocalPathSearching) {
                    setLocalPathBrowseExpandedKeys(keys);
                  }
                }}
              />
            </Form.Item>
          </>
        ) : selectedType === "feishu" ? (
          <Form.Item
            label={t("admin.dataSourceFeishuSpace")}
            name="target"
            getValueFromEvent={(value) =>
              collapseSelectedTreeValues(value, feishuTargetTreeData)
            }
            rules={[
              {
                validator: (_rule, value) => {
                  const values = Array.isArray(value)
                    ? value
                    : value
                      ? [value]
                      : [];
                  return values.length > 0
                    ? Promise.resolve()
                    : Promise.reject(
                        new Error(t("admin.dataSourceFeishuSpaceRequired")),
                      );
                },
              },
            ]}
          >
            <TreeSelect
              multiple
              allowClear
              filterTreeNode={false}
              loadData={onLoadFeishuTargetChildren}
              loading={feishuTargetLoading}
              maxTagCount="responsive"
              maxTagPlaceholder={renderFeishuTargetMaxTagPlaceholder}
              notFoundContent={
                feishuTargetLoading ? <Spin size="small" /> : null
              }
              placeholder={t("admin.dataSourceFeishuTargetPlaceholderWiki")}
              showSearch
              searchValue={feishuTargetSearchValue}
              style={{ width: "100%" }}
              tagRender={renderFeishuTargetTag}
              title={feishuTargetTitle}
              treeCheckable
              treeData={feishuTargetTreeData}
              treeExpandedKeys={feishuTargetExpandedKeys}
              treeLine
              onTreeExpand={(keys) => {
                setFeishuTargetExpandedKeys(keys);
                if (!isFeishuTargetSearching) {
                  setFeishuTargetBrowseExpandedKeys(keys);
                }
              }}
              showCheckedStrategy={TreeSelect.SHOW_PARENT}
              styles={{
                popup: { root: { maxHeight: 360, overflow: "auto" } },
              }}
              onOpenChange={(open) => {
                if (!open) {
                  setFeishuTargetSearchValue("");
                  // Keep browsed directory tree cached while the wizard stays open.
                  // Restore browse expansion after leaving search mode.
                  setFeishuTargetExpandedKeys(feishuTargetBrowseExpandedKeys);
                  onSearchFeishuTargetOptions?.("");
                  return;
                }
                onLoadFeishuTargetOptions?.();
                setFeishuTargetExpandedKeys(feishuTargetBrowseExpandedKeys);
              }}
              onSearch={(value) => {
                setFeishuTargetSearchValue(value);
                onSearchFeishuTargetOptions?.(value);
              }}
            />
          </Form.Item>
        ) : (
          <>
            <Form.Item
              label={t("admin.dataSourceNotionTargetTypeLabel")}
              name="targetType"
              rules={[
                {
                  required: true,
                  message: t("admin.dataSourceNotionTargetTypeRequired"),
                },
              ]}
            >
              <Radio.Group>
                <Radio.Button value="page">
                  {t("admin.dataSourceNotionTargetTypePage")}
                </Radio.Button>
                <Radio.Button value="database">
                  {t("admin.dataSourceNotionTargetTypeDatabase")}
                </Radio.Button>
              </Radio.Group>
            </Form.Item>
            <Form.Item
              label={t("admin.dataSourceNotionTargetLabel")}
              name="target"
              rules={[
                {
                  validator: (_rule, value) => {
                    const values = Array.isArray(value)
                      ? value
                      : value
                        ? [value]
                        : [];
                    return values
                      .map((item) => `${item || ""}`.trim())
                      .filter(Boolean).length > 0
                      ? Promise.resolve()
                      : Promise.reject(
                          new Error(t("admin.dataSourceNotionTargetRequired")),
                        );
                  },
                },
              ]}
            >
              <Input.TextArea
                placeholder={t("admin.dataSourceNotionTargetPlaceholder")}
                autoSize={{ minRows: 3, maxRows: 6 }}
              />
            </Form.Item>
          </>
        )}

        <Form.Item
          label={t("admin.dataSourceFileTypes")}
          name="fileTypes"
          rules={[
            {
              validator: (_rule, value) =>
                Array.isArray(value) && value.length > 0
                  ? Promise.resolve()
                  : Promise.reject(
                      new Error(t("admin.dataSourceFileTypesRequired")),
                    ),
            },
          ]}
          extra={t("admin.dataSourceFileTypesHint")}
        >
          <Select
            allowClear
            mode="multiple"
            maxTagCount={6}
            maxTagPlaceholder={renderFileTypeMaxTagPlaceholder}
            optionFilterProp="label"
            placeholder={t("admin.dataSourceFileTypesPlaceholder")}
            options={DATA_SOURCE_FILE_TYPE_OPTIONS.map((item) => ({
              label: t(item.i18nKey),
              value: item.value,
            }))}
          />
        </Form.Item>
      </section>

      <section className="data-source-form-section">
        <div className="data-source-form-section-title">
          {t("admin.dataSourceSyncStrategyTitle")}
        </div>
        <div className="data-source-strategy-section">
          <Text className="data-source-strategy-label">
            {t("admin.dataSourceSyncModeTitle")}
          </Text>
          <Form.Item name="syncMode" className="data-source-strategy-item">
            <Radio.Group className="data-source-sync-mode-pills">
              <Radio.Button value="scheduled">
                <div className="data-source-sync-mode-pill-content">
                  <Text strong>{t("admin.dataSourceSyncModeScheduled")}</Text>
                </div>
              </Radio.Button>
              <Radio.Button value="manual">
                <div className="data-source-sync-mode-pill-content">
                  <Text strong>{t("admin.dataSourceSyncModeManual")}</Text>
                </div>
              </Radio.Button>
            </Radio.Group>
          </Form.Item>
        </div>

        {syncMode === "scheduled" ? (
          <WizardSchedulePanel t={t} form={form} />
        ) : null}
      </section>
    </div>
  );
}
