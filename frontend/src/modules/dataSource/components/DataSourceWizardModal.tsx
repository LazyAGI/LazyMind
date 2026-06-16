import {
  Button,
  Checkbox,
  Empty,
  Form,
  Input,
  Modal,
  Radio,
  Space,
  Spin,
  Steps,
  Tag,
  TimePicker,
  TreeSelect,
  Typography,
} from "antd";
import type { FormInstance } from "antd";
import type { DataNode } from "antd/es/tree";
import type { TreeSelectProps } from "antd";
import dayjs from "dayjs";
import { useState } from "react";
import type { ReactNode } from "react";
import {
  ApiOutlined,
  CalendarOutlined,
  ClockCircleOutlined,
  DisconnectOutlined,
  FolderOpenOutlined,
  LinkOutlined,
  LockOutlined,
} from "@ant-design/icons";
import type {
  SourceFormValues,
  SourceType,
  SyncMode,
} from "../shared";
import {
  getSourceTypeDescription,
  getSourceTypeTitle,
} from "../shared";

const { Paragraph, Text } = Typography;

const SCHEDULE_WEEKDAYS = ["1", "2", "3", "4", "5", "6", "7"];
const SCHEDULE_WEEKDAY_DISPLAY_ORDER = ["7", "1", "2", "3", "4", "5", "6"];
const SCHEDULE_WORKDAYS = ["1", "2", "3", "4", "5"];
const SCHEDULE_WEEKENDS = ["6", "7"];

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
];

interface DataSourceWizardModalProps {
  t: any;
  wizardMode: "create" | "edit";
  wizardOpen: boolean;
  wizardStep: number;
  form: FormInstance<SourceFormValues>;
  selectedType: SourceType | null;
  isFeishuSetupReady: boolean;
  connectionVerified: boolean;
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
  onTestConnection: () => void;
  onInvalidateConnection: () => void;
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
  connectionVerified,
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
  onTestConnection,
  onInvalidateConnection,
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

  const renderConnectionSection = () => {
    if (!selectedType) {
      return null;
    }

    if (selectedType !== "local") {
      return null;
    }

    return (
      <div className="data-source-connect-card">
        <div className="data-source-connect-header">
          <div>
            <Text strong>{t("admin.dataSourceConnectionTest")}</Text>
          </div>
          <Tag color={connectionVerified ? "success" : "default"}>
            {connectionVerified
              ? t("admin.dataSourceConnectionVerified")
              : t("admin.dataSourceConnectionPending")}
          </Tag>
        </div>
        <Button
          type="primary"
          icon={<LinkOutlined />}
          disabled={isEditMode}
          onClick={onTestConnection}
        >
          {t("admin.dataSourceConnectionTestAction")}
        </Button>
      </div>
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
                return (
                  <button
                    key={item.type}
                    type="button"
                    className={`data-source-type-card ${
                      selectedType === item.type ? "selected" : ""
                    } ${isFeishuLocked ? "locked" : ""}`}
                    onClick={() => onSelectType(item.type)}
                  >
                    <div className="data-source-type-card-header">
                      <span className={`data-source-icon data-source-icon-${item.type}`}>
                        {item.icon}
                      </span>
                      <Space size={6}>
                        {item.type === "feishu" ? (
                          isFeishuLocked ? (
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
                                onResetFeishuSetup();
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
                  rules={[
                    {
                      required: true,
                      whitespace: true,
                      message: t("admin.dataSourceKnowledgeBaseNameRequired"),
                    },
                  ]}
                >
                  <Input
                    disabled={isEditMode}
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
                      onChange={() => {
                        if (!isEditMode) {
                          onInvalidateConnection();
                        }
                      }}
                      onOpenChange={(open) => {
                        if (!open) {
                          setLocalPathSearchValue("");
                        }
                        if (open && !isEditMode) {
                          onLoadLocalPathOptions?.("");
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
                ) : (
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
                      placeholder={t("admin.dataSourceFeishuTargetPlaceholderWiki")}
                      showSearch
                      searchValue={feishuTargetSearchValue}
                      style={{ width: "100%" }}
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
                )}

                {selectedType === "local" ? renderConnectionSection() : null}
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
