import {
  Button,
  Card,
  Checkbox,
  Col,
  Empty,
  Form,
  Input,
  Modal,
  Radio,
  Row,
  Space,
  Spin,
  Steps,
  Tag,
  TreeSelect,
  Typography,
} from "antd";
import type { FormInstance } from "antd";
import type { DataNode } from "antd/es/tree";
import type { TreeSelectProps } from "antd";
import { useState } from "react";
import type { ReactNode } from "react";
import {
  ApiOutlined,
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
const SCHEDULE_WORKDAYS = ["1", "2", "3", "4", "5"];
const SCHEDULE_WEEKENDS = ["6", "7"];

function normalizeScheduleWeekdays(value?: string[]) {
  return Array.from(new Set(value || []))
    .filter((day) => SCHEDULE_WEEKDAYS.includes(day))
    .sort((left, right) => Number(left) - Number(right));
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
  existingKnowledgeBaseNames: string[];
  selectedType: SourceType | null;
  isFeishuSetupReady: boolean;
  connectionVerified: boolean;
  syncMode: SyncMode;
  saving: boolean;
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
  existingKnowledgeBaseNames,
  selectedType,
  isFeishuSetupReady,
  connectionVerified,
  syncMode,
  saving,
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
  const scheduleWeekdays = Form.useWatch("scheduleWeekdays", form) || [];
  const scheduleTime = Form.useWatch("scheduleTime", form);
  const normalizedScheduleWeekdays = normalizeScheduleWeekdays(scheduleWeekdays);
  const scheduleDaysText =
    normalizedScheduleWeekdays.length === SCHEDULE_WEEKDAYS.length
      ? t("admin.dataSourceScheduleEveryday")
      : normalizedScheduleWeekdays
          .map((day) => t(`admin.dataSourceScheduleWeekday${day}`))
          .join("、") || t("admin.dataSourceScheduleNoDaysSelected");
  const scheduleTimeText = scheduleTime || t("admin.dataSourceScheduleNoTimeSelected");
  const existingKnowledgeBaseNameSet = new Set(
    existingKnowledgeBaseNames.map((name) => name.trim().toLowerCase()).filter(Boolean),
  );

  const validateKnowledgeBaseName = (_: unknown, value?: string) => {
    const normalizedValue = `${value || ""}`.trim().toLowerCase();
    if (!normalizedValue || isEditMode) {
      return Promise.resolve();
    }
    if (existingKnowledgeBaseNameSet.has(normalizedValue)) {
      return Promise.reject(new Error(t("admin.dataSourceKnowledgeBaseNameDuplicated")));
    }
    return Promise.resolve();
  };

  const renderConnectionSection = () => {
    if (!selectedType) {
      return null;
    }

    if (selectedType !== "local") {
      return null;
    }

    return (
      <Card size="small" className="data-source-connect-card">
        <div className="data-source-connect-header">
          <div>
            <Text strong>{t("admin.dataSourceConnectionTest")}</Text>
            <Paragraph type="secondary">{t("admin.dataSourceConnectionTestDesc")}</Paragraph>
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
      </Card>
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
                <Button disabled={saving} onClick={() => onSave("create")}>
                  {isEditMode
                    ? t("admin.dataSourceSaveOnly")
                    : t("admin.dataSourceCreateOnly")}
                </Button>
                <Button
                  type="primary"
                  loading={saving}
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
              <Row gutter={[16, 16]}>
                <Col xs={24}>
                  <Card className="data-source-form-card" title={t("admin.dataSourceBasicConfig")}>
                    <Form.Item
                      label={t("admin.dataSourceKnowledgeBaseName")}
                      name="knowledgeBase"
                      extra={
                        selectedType === "local"
                          ? t("admin.dataSourceKnowledgeBaseNameLocalHint")
                          : t("admin.dataSourceKnowledgeBaseNameHint")
                      }
                      rules={[
                        {
                          required: true,
                          whitespace: true,
                          message: t("admin.dataSourceKnowledgeBaseNameRequired"),
                        },
                        {
                          validator: validateKnowledgeBaseName,
                        },
                      ]}
                    >
                      <Input
                        disabled={isEditMode}
                        placeholder={t("admin.dataSourceKnowledgeBaseNamePlaceholder")}
                      />
                    </Form.Item>
                  </Card>

                  <Card className="data-source-form-card" title={t("admin.dataSourceAccessConfig")}>
                    {selectedType === "local" ? (
                      <Form.Item
                        label={t("admin.dataSourceAccessPath")}
                        name="path"
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
                  </Card>

                  <Card
                    className="data-source-form-card"
                    title={t("admin.dataSourceSyncStrategyTitle")}
                  >
                    <div className="data-source-strategy-section">
                      <Text className="data-source-strategy-label">
                        {t("admin.dataSourceSyncModeTitle")}
                      </Text>
                      <Form.Item name="syncMode" className="data-source-strategy-item">
                        <Radio.Group className="data-source-sync-mode-pills">
                          <Radio.Button value="scheduled">
                            <div className="data-source-sync-mode-pill-content">
                              <Text strong>{t("admin.dataSourceSyncModeScheduled")}</Text>
                              <Text type="secondary">
                                {t("admin.dataSourceSyncModeScheduledDesc")}
                              </Text>
                            </div>
                          </Radio.Button>
                          <Radio.Button value="manual">
                            <div className="data-source-sync-mode-pill-content">
                              <Text strong>{t("admin.dataSourceSyncModeManual")}</Text>
                              <Text type="secondary">
                                {t("admin.dataSourceSyncModeManualDesc")}
                              </Text>
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
                          <Text type="secondary">{t("admin.dataSourceScheduleDesc")}</Text>
                        </div>
                        <Row gutter={16}>
                          <Col xs={24}>
                            <Form.Item
                              label={t("admin.dataSourceScheduleWeekdays")}
                              name="scheduleWeekdays"
                              rules={[
                                {
                                  required: true,
                                  message: t("admin.dataSourceScheduleWeekdaysRequired"),
                                },
                              ]}
                            >
                              <Checkbox.Group className="data-source-schedule-weekdays">
                                {SCHEDULE_WEEKDAYS.map((day) => (
                                  <Checkbox key={day} value={day}>
                                    {t(`admin.dataSourceScheduleWeekday${day}`)}
                                  </Checkbox>
                                ))}
                              </Checkbox.Group>
                            </Form.Item>
                            <Space wrap className="data-source-schedule-shortcuts">
                              <Button
                                size="small"
                                onClick={() =>
                                  form.setFieldValue("scheduleWeekdays", SCHEDULE_WORKDAYS)
                                }
                              >
                                {t("admin.dataSourceScheduleShortcutWorkdays")}
                              </Button>
                              <Button
                                size="small"
                                onClick={() =>
                                  form.setFieldValue("scheduleWeekdays", SCHEDULE_WEEKENDS)
                                }
                              >
                                {t("admin.dataSourceScheduleShortcutWeekends")}
                              </Button>
                              <Button
                                size="small"
                                onClick={() =>
                                  form.setFieldValue("scheduleWeekdays", SCHEDULE_WEEKDAYS)
                                }
                              >
                                {t("admin.dataSourceScheduleShortcutEveryday")}
                              </Button>
                            </Space>
                            <div className="data-source-schedule-summary">
                              <ClockCircleOutlined />
                              <Text>
                                {t("admin.dataSourceScheduleSummary", {
                                  days: scheduleDaysText,
                                  time: scheduleTimeText,
                                })}
                              </Text>
                            </div>
                          </Col>
                          <Col xs={24} md={12}>
                            <Form.Item
                              label={t("admin.dataSourceScheduleTime")}
                              name="scheduleTime"
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
                              <Input
                                type="time"
                                min="00:00"
                                max="23:59:59"
                                step={1}
                              />
                            </Form.Item>
                          </Col>
                        </Row>
                      </div>
                    ) : null}
                  </Card>
                </Col>
              </Row>
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
