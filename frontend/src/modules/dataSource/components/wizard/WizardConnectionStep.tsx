import { useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import {
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
import type { DataNode } from "antd/es/tree";
import type { TFunction } from "i18next";
import {
  KNOWLEDGE_BASE_NAME_MAX_LENGTH,
  KNOWLEDGE_BASE_NAME_PATTERN,
} from "@/modules/knowledge/constants/validation";
import { DATA_SOURCE_FILE_TYPE_OPTIONS } from "../../constants/options";
import type { SourceFormValues, SourceType, SyncMode } from "../../constants/types";
import {
  buildTreeValuePathMap,
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
import WizardSchedulePanel from "./WizardSchedulePanel";

const { Text } = Typography;

export interface WizardConnectionStepProps {
  t: TFunction;
  form: FormInstance<SourceFormValues>;
  isEditMode: boolean;
  selectedType: SourceType;
  syncMode: SyncMode;
  localPathOptions: LocalPathSelectOption[];
  localPathLoading: boolean;
  feishuTargetLoading: boolean;
  feishuTargetTreeData: DataNode[];
  onLoadLocalPathOptions?: (path?: string) => void;
  onSearchLocalPathOptions?: (keyword: string) => void;
  onLoadLocalPathChildren?: TreeSelectProps["loadData"];
  onResetLocalPathBrowseOptions?: () => void;
  onLoadFeishuTargetOptions?: () => void;
  onSearchFeishuTargetOptions?: (keyword: string) => void;
  onLoadFeishuTargetChildren?: TreeSelectProps["loadData"];
  onResetFeishuTargetBrowseOptions?: () => void;
}

export default function WizardConnectionStep({
  t,
  form,
  isEditMode,
  selectedType,
  syncMode,
  localPathOptions,
  localPathLoading,
  feishuTargetLoading,
  feishuTargetTreeData,
  onLoadLocalPathOptions,
  onSearchLocalPathOptions,
  onLoadLocalPathChildren,
  onResetLocalPathBrowseOptions,
  onLoadFeishuTargetOptions,
  onSearchFeishuTargetOptions,
  onLoadFeishuTargetChildren,
  onResetFeishuTargetBrowseOptions,
}: WizardConnectionStepProps) {
  const [localPathSearchValue, setLocalPathSearchValue] = useState("");
  const [feishuTargetSearchValue, setFeishuTargetSearchValue] = useState("");
  const [feishuTargetExpandedKeys, setFeishuTargetExpandedKeys] = useState<
    Array<string | number>
  >([]);
  const [localPathBrowseKey, setLocalPathBrowseKey] = useState(0);
  const [feishuTargetBrowseKey, setFeishuTargetBrowseKey] = useState(0);

  const isFeishuTargetSearching = Boolean(feishuTargetSearchValue.trim());

  useEffect(() => {
    if (!isFeishuTargetSearching) {
      setFeishuTargetExpandedKeys([]);
      return;
    }
    if (feishuTargetLoading) {
      return;
    }
    setFeishuTargetExpandedKeys(
      collectTreeExpandableKeys(feishuTargetTreeData as CollapsibleTreeNode[]),
    );
  }, [feishuTargetTreeData, feishuTargetLoading, isFeishuTargetSearching]);

  const localPathValue = Form.useWatch("path", form);
  const selectedLocalPathValues = normalizeTreeSelectValues(localPathValue);
  const feishuTargetPathMap = useMemo(
    () => buildTreeValuePathMap(feishuTargetTreeData as CollapsibleTreeNode[]),
    [feishuTargetTreeData],
  );
  const selectedFeishuTargetValues = normalizeTreeSelectValues(
    Form.useWatch("target", form),
  );
  const feishuTargetTitle = selectedFeishuTargetValues
    .map((value) => feishuTargetPathMap.get(value) || value)
    .filter(Boolean)
    .join("\n");
  const fileTypeLabelMap = useMemo(
    () =>
      new Map(
        DATA_SOURCE_FILE_TYPE_OPTIONS.map((item) => [item.value, t(item.i18nKey)]),
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
      .map((item) => fileTypeLabelMap.get(`${item.value || ""}` as any) || item.label)
      .filter(Boolean);

    return (
      <Tooltip
        title={
          <div className="data-source-tree-select-tooltip-list">
            {labels.map((label, index) => (
              <div key={`${getTreeSelectLabelText(label)}-${index}`}>{label}</div>
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
    <Tooltip title={getFeishuTargetValuePath(value, label, feishuTargetPathMap, t)}>
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
          {getFeishuTargetDisplayText(value, label, t)}
        </span>
      </Tag>
    </Tooltip>
  );

  const renderFeishuTargetMaxTagPlaceholder: TreeSelectProps["maxTagPlaceholder"] = (
    omittedValues,
  ) => {
    if (omittedValues.length === 0) {
      return null;
    }

    const paths = omittedValues
      .map((item) =>
        getFeishuTargetValuePath(item.value, item.label, feishuTargetPathMap, t),
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
            disabled={isEditMode}
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
          <Form.Item
            label={t("admin.dataSourceAccessPath")}
            name="path"
            getValueFromEvent={(value) =>
              collapseSelectedTreeValues(value, localPathOptions)
            }
            rules={[
              {
                validator: (_rule, value) => {
                  const values = Array.isArray(value) ? value : value ? [value] : [];
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
              disabled={isEditMode}
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
              treeLine
              showCheckedStrategy={TreeSelect.SHOW_PARENT}
              styles={{
                popup: { root: { maxHeight: 360, overflow: "auto" } },
              }}
              onOpenChange={(open) => {
                if (!open) {
                  setLocalPathSearchValue("");
                  setLocalPathBrowseKey((key) => key + 1);
                  onResetLocalPathBrowseOptions?.();
                  return;
                }
                if (!isEditMode) {
                  onLoadLocalPathOptions?.(
                    selectedLocalPathValues.length === 1
                      ? selectedLocalPathValues[0]
                      : "",
                  );
                }
              }}
              onSearch={(value) => {
                setLocalPathSearchValue(value);
                if (!isEditMode) {
                  onSearchLocalPathOptions?.(value);
                }
              }}
            />
          </Form.Item>
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
                  const values = Array.isArray(value) ? value : value ? [value] : [];
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
              key={`feishu-target-browse-${feishuTargetBrowseKey}`}
              multiple
              allowClear
              disabled={isEditMode}
              filterTreeNode={false}
              loadData={onLoadFeishuTargetChildren}
              loading={feishuTargetLoading}
              maxTagCount="responsive"
              maxTagPlaceholder={renderFeishuTargetMaxTagPlaceholder}
              notFoundContent={feishuTargetLoading ? <Spin size="small" /> : null}
              placeholder={t("admin.dataSourceFeishuTargetPlaceholderWiki")}
              showSearch
              searchValue={feishuTargetSearchValue}
              style={{ width: "100%" }}
              tagRender={renderFeishuTargetTag}
              title={feishuTargetTitle}
              treeCheckable
              treeData={feishuTargetTreeData}
              treeExpandedKeys={
                isFeishuTargetSearching ? feishuTargetExpandedKeys : undefined
              }
              treeLine
              onTreeExpand={
                isFeishuTargetSearching ? setFeishuTargetExpandedKeys : undefined
              }
              showCheckedStrategy={TreeSelect.SHOW_PARENT}
              styles={{
                popup: { root: { maxHeight: 360, overflow: "auto" } },
              }}
              onOpenChange={(open) => {
                if (!open) {
                  setFeishuTargetSearchValue("");
                  setFeishuTargetBrowseKey((key) => key + 1);
                  onResetFeishuTargetBrowseOptions?.();
                  return;
                }
                if (!isEditMode) {
                  onLoadFeishuTargetOptions?.();
                }
              }}
              onSearch={(value) => {
                setFeishuTargetSearchValue(value);
                if (!isEditMode) {
                  onSearchFeishuTargetOptions?.(value);
                }
              }}
            />
          </Form.Item>
        ) : (
          <>
            <Form.Item
              label={t("admin.dataSourceNotionTargetTypeLabel")}
              name="targetType"
              rules={[{ required: true, message: t("admin.dataSourceNotionTargetTypeRequired") }]}
            >
              <Radio.Group disabled={isEditMode}>
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
                    const values = Array.isArray(value) ? value : value ? [value] : [];
                    return values
                      .map((item) => `${item || ""}`.trim())
                      .filter(Boolean).length > 0
                      ? Promise.resolve()
                      : Promise.reject(new Error(t("admin.dataSourceNotionTargetRequired")));
                  },
                },
              ]}
            >
              <Input.TextArea
                disabled={isEditMode}
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

        {syncMode === "scheduled" ? <WizardSchedulePanel t={t} form={form} /> : null}
      </section>
    </div>
  );
}
