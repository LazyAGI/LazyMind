import {
  Button,
  Checkbox,
  Empty,
  Form,
  Input,
  Modal,
  Radio,
  Select,
  Space,
  Spin,
  Steps,
  Tag,
  TimePicker,
  Tooltip,
  TreeSelect,
  Typography,
} from "antd";
import type { FormInstance } from "antd";
import type { DataNode } from "antd/es/tree";
import type { TreeSelectProps } from "antd";
import dayjs from "dayjs";
import { useMemo, useState } from "react";
import type { ReactNode } from "react";
import {
  ApiOutlined,
  CalendarOutlined,
  ClockCircleOutlined,
  DatabaseOutlined,
  DisconnectOutlined,
  FolderOpenOutlined,
  LockOutlined,
} from "@ant-design/icons";
import type {
  SourceFormValues,
  SourceType,
  SyncMode,
} from "../shared";
import {
  DATA_SOURCE_FILE_TYPE_OPTIONS,
  getSourceTypeDescription,
  getSourceTypeTitle,
} from "../shared";
import {
  KNOWLEDGE_BASE_NAME_MAX_LENGTH,
  KNOWLEDGE_BASE_NAME_PATTERN,
} from "@/modules/knowledge/constants/validation";

const { Paragraph, Text } = Typography;

const SCHEDULE_WEEKDAYS = ["1", "2", "3", "4", "5", "6", "7"];
const SCHEDULE_WEEKDAY_DISPLAY_ORDER = ["7", "1", "2", "3", "4", "5", "6"];
const SCHEDULE_WORKDAYS = ["1", "2", "3", "4", "5"];
const SCHEDULE_WEEKENDS = ["6", "7"];
const FEISHU_MANUAL_TARGET_VALUE_PREFIX = "__scan-feishu-manual-target__";

function normalizeSelectedWeekdays(value?: string[]) {
  return Array.from(new Set(value || []))
    .filter((day) => SCHEDULE_WEEKDAYS.includes(day))
    .sort((left, right) => Number(left) - Number(right));
}

function isSameWeekdaySet(left: string[], right: string[]) {
  if (left.length !== right.length) {
    return false;
  }

  return left.every((value, index) => value === right[index]);
}

function toggleShortcutWeekdays(
  current: string[],
  target: string[],
) {
  return isSameWeekdaySet(current, target) ? [] : target;
}

type CollapsibleTreeNode = DataNode & {
  value?: string | number;
  children?: CollapsibleTreeNode[];
};

function normalizeTreeSelectValues(value: unknown) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values.map((item) => `${item || ""}`.trim()).filter(Boolean);
}

function collectDescendantValues(
  nodes: CollapsibleTreeNode[] | undefined,
  descendantValues: Set<string>,
) {
  nodes?.forEach((node) => {
    const nodeValue = `${node.value || ""}`.trim();
    if (nodeValue) {
      descendantValues.add(nodeValue);
    }
    collectDescendantValues(node.children, descendantValues);
  });
}

function collapseSelectedTreeValues(
  value: unknown,
  treeData: CollapsibleTreeNode[],
) {
  const values = normalizeTreeSelectValues(value);
  const selectedValues = new Set(values);
  const descendantValues = new Set<string>();

  const visit = (nodes: CollapsibleTreeNode[]) => {
    nodes.forEach((node) => {
      const nodeValue = `${node.value || ""}`.trim();
      if (nodeValue && selectedValues.has(nodeValue)) {
        collectDescendantValues(node.children, descendantValues);
        return;
      }
      if (node.children) {
        visit(node.children);
      }
    });
  };

  visit(treeData);
  return values.filter((item) => !descendantValues.has(item));
}

function getTreeNodeTitleText(node: CollapsibleTreeNode) {
  const title = node.title;
  if (typeof title === "string" || typeof title === "number") {
    return `${title}`.trim();
  }
  return `${node.value || node.key || ""}`.trim();
}

function buildTreeValuePathMap(treeData: CollapsibleTreeNode[]) {
  const pathMap = new Map<string, string>();

  const visit = (nodes: CollapsibleTreeNode[], parentTitles: string[]) => {
    nodes.forEach((node) => {
      const nodeValue = `${node.value || node.key || ""}`.trim();
      const title = getTreeNodeTitleText(node);
      const nextTitles = title ? [...parentTitles, title] : parentTitles;
      if (nodeValue) {
        pathMap.set(nodeValue, nextTitles.join(" / ") || nodeValue);
      }
      if (node.children) {
        visit(node.children, nextTitles);
      }
    });
  };

  visit(treeData, []);
  return pathMap;
}

function getTreeSelectLabelText(label: ReactNode) {
  if (typeof label === "string" || typeof label === "number") {
    return `${label}`.trim();
  }
  return "";
}

function getTreeSelectValuePath(
  value: unknown,
  label: ReactNode,
  pathMap: Map<string, string>,
) {
  const normalizedValue = `${value || ""}`.trim();
  return pathMap.get(normalizedValue) || getTreeSelectLabelText(label) || normalizedValue;
}

function parseManualFeishuTargetValue(value: unknown) {
  const normalizedValue = `${value || ""}`.trim();
  if (!normalizedValue.startsWith(`${FEISHU_MANUAL_TARGET_VALUE_PREFIX}:`)) {
    return null;
  }

  const parts = normalizedValue.split(":");
  const kind = parts[1] || "";
  const encodedTargetRef = parts.slice(2).join(":");
  if (!["current", "wiki", "drive"].includes(kind)) {
    return null;
  }

  let targetRef = encodedTargetRef;
  try {
    targetRef = decodeURIComponent(encodedTargetRef);
  } catch {
  }

  const normalizedTargetRef = targetRef.trim();
  return normalizedTargetRef ? { kind, targetRef: normalizedTargetRef } : null;
}

function formatManualFeishuTargetLabel(
  value: unknown,
  t: DataSourceWizardModalProps["t"],
) {
  const parsed = parseManualFeishuTargetValue(value);
  if (!parsed) {
    return null;
  }
  if (parsed.kind === "wiki") {
    return t("admin.dataSourceUseCurrentFeishuWikiInput", {
      value: parsed.targetRef,
    });
  }
  if (parsed.kind === "drive") {
    return t("admin.dataSourceUseCurrentFeishuDriveInput", {
      value: parsed.targetRef,
    });
  }
  return t("admin.dataSourceUseCurrentInput", {
    value: parsed.targetRef,
  });
}

function getFeishuTargetDisplayText(
  value: unknown,
  label: ReactNode,
  t: DataSourceWizardModalProps["t"],
) {
  const labelText = getTreeSelectLabelText(label);
  if (labelText && !labelText.startsWith(FEISHU_MANUAL_TARGET_VALUE_PREFIX)) {
    return labelText;
  }
  return formatManualFeishuTargetLabel(value, t) || labelText;
}

function getFeishuTargetValuePath(
  value: unknown,
  label: ReactNode,
  pathMap: Map<string, string>,
  t: DataSourceWizardModalProps["t"],
) {
  const normalizedValue = `${value || ""}`.trim();
  return (
    pathMap.get(normalizedValue) ||
    getFeishuTargetDisplayText(value, label, t) ||
    normalizedValue
  );
}

export type LocalPathSelectOption = DataNode & {
  value: string;
  nodeRef?: string;
  targetRef?: string;
  children?: LocalPathSelectOption[];
};

const sourceTypeOptions: Array<{
  type: SourceType;
  icon: ReactNode;
  adminOnly?: boolean;
}> = [
  {
    type: "local",
    icon: <FolderOpenOutlined />,
    adminOnly: true,
  },
  {
    type: "feishu",
    icon: <ApiOutlined />,
  },
  {
    type: "notion",
    icon: <DatabaseOutlined />,
  },
];

interface DataSourceWizardModalProps {
  t: any;
  wizardMode: "create" | "edit";
  wizardOpen: boolean;
  wizardStep: number;
  form: FormInstance<SourceFormValues>;
  selectedType: SourceType | null;
  isFeishuSetupReady: boolean;
  isNotionSetupReady?: boolean;
  syncMode: SyncMode;
  saving: boolean;
  savingMode?: "create" | "createAndSync";
  localPathOptions?: LocalPathSelectOption[];
  localPathLoading?: boolean;
  feishuTargetLoading?: boolean;
  feishuTargetTreeData?: DataNode[];
  allowTypeSelection?: boolean;
  onClose: () => void;
  onPrev: () => void;
  onNext: () => void;
  onSave: (mode: "create" | "createAndSync") => void;
  onSelectType: (type: SourceType) => void;
  onResetFeishuSetup: () => void;
  onResetNotionSetup?: () => void;
  onLoadLocalPathOptions?: (path?: string) => void;
  onSearchLocalPathOptions?: (keyword: string) => void;
  onLoadLocalPathChildren?: TreeSelectProps["loadData"];
  onLoadFeishuTargetOptions?: () => void;
  onSearchFeishuTargetOptions?: (keyword: string) => void;
  onLoadFeishuTargetChildren?: TreeSelectProps["loadData"];
}

export default function DataSourceWizardModal({
  t,
  wizardMode,
  wizardOpen,
  wizardStep,
  form,
  selectedType,
  isFeishuSetupReady,
  isNotionSetupReady = false,
  syncMode,
  saving,
  savingMode,
  localPathOptions = [],
  localPathLoading = false,
  feishuTargetLoading = false,
  feishuTargetTreeData = [],
  allowTypeSelection = true,
  onClose,
  onPrev,
  onNext,
  onSave,
  onSelectType,
  onResetFeishuSetup,
  onResetNotionSetup,
  onLoadLocalPathOptions,
  onSearchLocalPathOptions,
  onLoadLocalPathChildren,
  onLoadFeishuTargetOptions,
  onSearchFeishuTargetOptions,
  onLoadFeishuTargetChildren,
}: DataSourceWizardModalProps) {
  const isEditMode = wizardMode === "edit";
  const [localPathSearchValue, setLocalPathSearchValue] = useState("");
  const [feishuTargetSearchValue, setFeishuTargetSearchValue] = useState("");
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
  const selectedScheduleWeekdays = normalizeSelectedWeekdays(
    Form.useWatch("scheduleWeekdays", form),
  );
  const isWorkdaysSelected = isSameWeekdaySet(
    selectedScheduleWeekdays,
    SCHEDULE_WORKDAYS,
  );
  const isWeekendsSelected = isSameWeekdaySet(
    selectedScheduleWeekdays,
    SCHEDULE_WEEKENDS,
  );
  const isEverydaySelected = isSameWeekdaySet(
    selectedScheduleWeekdays,
    SCHEDULE_WEEKDAYS,
  );
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
    <Modal
      title={wizardMode === "edit" ? t("admin.dataSourceEdit") : t("admin.dataSourceCreate")}
      open={wizardOpen}
      width={980}
      onCancel={() => {
        if (!saving) {
          onClose();
        }
      }}
      destroyOnHidden
      maskClosable={false}
      footer={
        <div className="data-source-wizard-footer">
          <Button disabled={saving} onClick={onClose}>{t("common.cancel")}</Button>
          <Space wrap>
            {allowTypeSelection && wizardStep > 0 && !isEditMode ? (
              <Button disabled={saving} onClick={onPrev}>{t("admin.dataSourceWizardPrev")}</Button>
            ) : null}
            {wizardStep < 1 ? (
              <Button type="primary" disabled={saving} onClick={onNext}>
                {t("admin.dataSourceWizardNext")}
              </Button>
            ) : null}
            {wizardStep === 1 ? (
              <>
                <Button
                  disabled={saving}
                  loading={savingMode === "create"}
                  onClick={() => onSave("create")}
                >
                  {isEditMode
                    ? t("admin.dataSourceSaveOnly")
                    : t("admin.dataSourceCreateOnly")}
                </Button>
                <Button
                  type="primary"
                  disabled={saving}
                  loading={savingMode === "createAndSync"}
                  onClick={() => onSave("createAndSync")}
                >
                  {isEditMode
                    ? t("admin.dataSourceSaveAndSync")
                    : t("admin.dataSourceCreateAndSync")}
                </Button>
              </>
            ) : null}
          </Space>
        </div>
      }
    >
      {!isEditMode && allowTypeSelection ? (
        <Steps
          current={wizardStep}
          items={[
            { title: t("admin.dataSourceWizardType") },
            { title: t("admin.dataSourceWizardConnection") },
          ]}
          className="data-source-wizard-steps"
        />
      ) : null}

      <Form form={form} layout="vertical" className="data-source-wizard-form">
        {allowTypeSelection && wizardStep === 0 ? (
          <div>
            <Paragraph type="secondary" className="data-source-wizard-intro">
              {t("admin.dataSourceTypeStepIntro")}
            </Paragraph>
            <div className="data-source-type-grid">
              {sourceTypeOptions.map((item) => {
                const isFeishuLocked = item.type === "feishu" && !isFeishuSetupReady;
                const isNotionLocked = item.type === "notion" && !isNotionSetupReady;
                const isCloudLocked = isFeishuLocked || isNotionLocked;
                return (
                  <button
                    key={item.type}
                    type="button"
                    className={`data-source-type-card ${
                      selectedType === item.type ? "selected" : ""
                    } ${isCloudLocked ? "locked" : ""}`}
                    onClick={() => onSelectType(item.type)}
                  >
                    <div className="data-source-type-card-header">
                      <span className={`data-source-icon data-source-icon-${item.type}`}>
                        {item.icon}
                      </span>
                      <Space size={6}>
                        {item.type === "feishu" || item.type === "notion" ? (
                          isCloudLocked ? (
                            <span className="data-source-type-gate-icon locked" aria-hidden="true">
                              <LockOutlined />
                            </span>
                          ) : (
                            <Button
                              type="text"
                              size="small"
                              className="data-source-type-gate-button"
                              icon={<DisconnectOutlined />}
                              onClick={(event) => {
                                event.preventDefault();
                                event.stopPropagation();
                                if (item.type === "feishu") {
                                  onResetFeishuSetup();
                                } else {
                                  onResetNotionSetup?.();
                                }
                              }}
                            />
                          )
                        ) : null}
                        {item.adminOnly ? (
                          <Tag color="orange">{t("admin.dataSourceAdminOnly")}</Tag>
                        ) : null}
                      </Space>
                    </div>
                    <Text strong>{getSourceTypeTitle(item.type, t)}</Text>
                    <Text type="secondary">
                      {item.type === "feishu" && isFeishuLocked
                        ? t("admin.dataSourceFeishuLockHint")
                        : item.type === "notion" && isNotionLocked
                          ? t("admin.dataSourceNotionSetupRequiredForCreate")
                        : getSourceTypeDescription(item.type, t)}
                    </Text>
                  </button>
                );
              })}
            </div>
          </div>
        ) : null}

        {wizardStep === 1 ? (
          selectedType ? (
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
                        }
                        if (open && !isEditMode) {
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
                      multiple
                      allowClear
                      disabled={isEditMode}
                      filterTreeNode={false}
                      loadData={onLoadFeishuTargetChildren}
                      loading={feishuTargetLoading}
                      maxTagCount="responsive"
                      maxTagPlaceholder={renderFeishuTargetMaxTagPlaceholder}
                      placeholder={t("admin.dataSourceFeishuTargetPlaceholderWiki")}
                      showSearch
                      searchValue={feishuTargetSearchValue}
                      style={{ width: "100%" }}
                      tagRender={renderFeishuTargetTag}
                      title={feishuTargetTitle}
                      treeCheckable
                      treeData={feishuTargetTreeData}
                      treeDefaultExpandAll={false}
                      treeLine
                      showCheckedStrategy={TreeSelect.SHOW_PARENT}
                      styles={{
                        popup: { root: { maxHeight: 360, overflow: "auto" } },
                      }}
                      onOpenChange={(open) => {
                        if (!open) {
                          setFeishuTargetSearchValue("");
                        }
                        if (open && !isEditMode) {
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

                {syncMode === "scheduled" ? (
                  <div className="data-source-schedule-panel">
                    <div className="data-source-schedule-panel-head">
                      <ClockCircleOutlined />
                      <Text strong>{t("admin.dataSourceScheduleTitle")}</Text>
                    </div>
                    <div className="data-source-schedule-inline-builder">
                      <div className="data-source-schedule-inline-toolbar">
                        <Space wrap className="data-source-schedule-shortcuts">
                          <Button
                            size="small"
                            className={isWorkdaysSelected ? "is-active" : ""}
                            onClick={() =>
                              form.setFieldValue(
                                "scheduleWeekdays",
                                toggleShortcutWeekdays(
                                  selectedScheduleWeekdays,
                                  SCHEDULE_WORKDAYS,
                                ),
                              )
                            }
                          >
                            {t("admin.dataSourceScheduleShortcutWorkdays")}
                          </Button>
                          <Button
                            size="small"
                            className={isWeekendsSelected ? "is-active" : ""}
                            onClick={() =>
                              form.setFieldValue(
                                "scheduleWeekdays",
                                toggleShortcutWeekdays(
                                  selectedScheduleWeekdays,
                                  SCHEDULE_WEEKENDS,
                                ),
                              )
                            }
                          >
                            {t("admin.dataSourceScheduleShortcutWeekends")}
                          </Button>
                          <Button
                            size="small"
                            className={isEverydaySelected ? "is-active" : ""}
                            onClick={() =>
                              form.setFieldValue(
                                "scheduleWeekdays",
                                toggleShortcutWeekdays(
                                  selectedScheduleWeekdays,
                                  SCHEDULE_WEEKDAYS,
                                ),
                              )
                            }
                          >
                            {t("admin.dataSourceScheduleShortcutEveryday")}
                          </Button>
                        </Space>
                      </div>
                      <div className="data-source-schedule-inline-sentence">
                        <div className="data-source-schedule-inline-icon">
                          <CalendarOutlined />
                          <ClockCircleOutlined />
                        </div>
                        <div className="data-source-schedule-inline-content">
                          <Text className="data-source-schedule-inline-prefix">
                            {t("admin.dataSourceScheduleSelectDaysPrefix")}
                          </Text>
                          <div className="data-source-schedule-inline-controls">
                            <Text className="data-source-schedule-inline-cycle">
                              {t("admin.dataSourceScheduleWeekly")}
                            </Text>
                            <Form.Item
                              name="scheduleWeekdays"
                              className="data-source-schedule-inline-weekdays-item"
                              rules={[
                                {
                                  required: true,
                                  message: t("admin.dataSourceScheduleWeekdaysRequired"),
                                },
                              ]}
                            >
                              <Checkbox.Group className="data-source-schedule-weekdays">
                                {SCHEDULE_WEEKDAY_DISPLAY_ORDER.map((day) => (
                                  <Checkbox key={day} value={day}>
                                    <span className="data-source-schedule-weekday-pill">
                                      {t(`admin.dataSourceScheduleWeekdayShort${day}`)}
                                    </span>
                                  </Checkbox>
                                ))}
                              </Checkbox.Group>
                            </Form.Item>
                            <Text className="data-source-schedule-inline-connector">
                              {t("admin.dataSourceScheduleTimeConnector")}
                            </Text>
                            <Form.Item
                              name="scheduleTime"
                              className="data-source-schedule-inline-time-item"
                              getValueProps={(value?: string) => ({
                                value: value ? dayjs(value, "HH:mm:ss") : null,
                              })}
                              normalize={(value: ReturnType<typeof dayjs> | null) =>
                                value ? value.format("HH:mm:ss") : undefined
                              }
                              rules={[
                                {
                                  required: true,
                                  message: t("admin.dataSourceScheduleTimeRequired"),
                                },
                                {
                                  pattern: /^([01]\d|2[0-3]):[0-5]\d:[0-5]\d$/,
                                  message: t("admin.dataSourceScheduleTimeInvalid"),
                                },
                              ]}
                            >
                              <TimePicker
                                className="data-source-schedule-time-picker"
                                format="HH:mm:ss"
                                needConfirm={false}
                                showNow={false}
                              />
                            </Form.Item>
                            <Text className="data-source-schedule-inline-suffix">
                              {t("admin.dataSourceScheduleTimeSuffix")}
                            </Text>
                          </div>
                        </div>
                        <div className="data-source-schedule-visual" aria-hidden="true">
                          <CalendarOutlined />
                          <ClockCircleOutlined />
                        </div>
                      </div>
                    </div>
                  </div>
                ) : null}
              </section>
            </div>
          ) : (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={t("admin.dataSourceSelectTypeInPrevStep")}
            />
          )
        ) : null}
      </Form>
    </Modal>
  );
}
