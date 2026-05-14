import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Empty,
  Form,
  Input,
  Modal,
  Radio,
  Row,
  Select,
  Space,
  Steps,
  Tag,
  Typography,
} from "antd";
import type { FormInstance } from "antd";
import type { ReactNode } from "react";
import {
  ApiOutlined,
  ClockCircleOutlined,
  DisconnectOutlined,
  FolderOpenOutlined,
  LinkOutlined,
  LockOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import type { FeishuDataSourceConnection } from "../feishuOAuth";
import type {
  FeishuTargetType,
  OAuthState,
  SourceFormValues,
  SourceType,
  SyncMode,
} from "../shared";
import {
  getConnectionMeta,
  getSourceTypeDescription,
  getSourceTypeTitle,
  isCloudType,
} from "../shared";

const { Paragraph, Text } = Typography;

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
  oauthState: OAuthState;
  oauthConnection: FeishuDataSourceConnection | null;
  connectionVerified: boolean;
  syncMode: SyncMode;
  feishuTargetType: FeishuTargetType;
  saving: boolean;
  onClose: () => void;
  onPrev: () => void;
  onNext: () => void;
  onSave: () => void;
  onSelectType: (type: SourceType) => void;
  onResetFeishuSetup: () => void;
  onConnectAccount: () => void;
  onOpenManualOauthModal: () => void;
  onTestConnection: () => void;
  onInvalidateConnection: () => void;
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
  oauthState,
  oauthConnection,
  connectionVerified,
  syncMode,
  feishuTargetType,
  saving,
  onClose,
  onPrev,
  onNext,
  onSave,
  onSelectType,
  onResetFeishuSetup,
  onConnectAccount,
  onOpenManualOauthModal,
  onTestConnection,
  onInvalidateConnection,
}: DataSourceWizardModalProps) {
  const isEditMode = wizardMode === "edit";
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

    if (isCloudType(selectedType)) {
      const meta = getConnectionMeta(oauthState, t);
      return (
        <Card size="small" className="data-source-connect-card">
          <div className="data-source-connect-header">
            <div>
              <Text strong>{t("admin.dataSourceOauthConnectTitle")}</Text>
              <Paragraph type="secondary">{t("admin.dataSourceOauthConnectDesc")}</Paragraph>
            </div>
            <Tag color={meta.color}>{meta.text}</Tag>
          </div>
          {!isFeishuSetupReady ? (
            <Alert
              showIcon
              type="info"
              message={t("admin.dataSourceFeishuNotReady")}
              description={t("admin.dataSourceFeishuNotReadyDesc")}
            />
          ) : null}
          <Space wrap>
            <Button
              type="primary"
              icon={oauthState === "waiting" ? <SyncOutlined spin /> : <LinkOutlined />}
              loading={oauthState === "waiting"}
              disabled={isEditMode || !isFeishuSetupReady}
              onClick={onConnectAccount}
            >
              {oauthConnection
                ? t("admin.dataSourceReconnectAccount")
                : t("admin.dataSourceConnectAccount")}
            </Button>
            {oauthState === "waiting" && !isEditMode ? (
              <Button onClick={onOpenManualOauthModal}>
                {t("admin.dataSourceOauthManualCallbackAction")}
              </Button>
            ) : null}
          </Space>
          {oauthState === "waiting" ? (
            <Alert
              showIcon
              type="info"
              message={t("admin.dataSourceOauthManualCallbackTitle")}
              description={t("admin.dataSourceOauthManualCallbackDesc")}
            />
          ) : null}
          {oauthConnection ? (
            <div className="data-source-oauth-meta">
              <Descriptions size="small" column={1} className="data-source-oauth-descriptions">
                <Descriptions.Item label={t("admin.dataSourceConnectedAccount")}>
                  {oauthConnection.accountName}
                </Descriptions.Item>
                {oauthConnection.tenantKey ? (
                  <Descriptions.Item label={t("admin.dataSourceTenantKey")}>
                    {oauthConnection.tenantKey}
                  </Descriptions.Item>
                ) : null}
                {oauthConnection.connectedAt ? (
                  <Descriptions.Item label={t("admin.dataSourceConnectedAt")}>
                    {oauthConnection.connectedAt}
                  </Descriptions.Item>
                ) : null}
                {oauthConnection.expiresAt ? (
                  <Descriptions.Item label={t("admin.dataSourceAccessTokenExpireAt")}>
                    {oauthConnection.expiresAt}
                  </Descriptions.Item>
                ) : null}
                {oauthConnection.refreshExpiresAt ? (
                  <Descriptions.Item label={t("admin.dataSourceRefreshTokenExpireAt")}>
                    {oauthConnection.refreshExpiresAt}
                  </Descriptions.Item>
                ) : null}
                {oauthConnection.accessTokenMasked || oauthConnection.refreshTokenMasked ? (
                  <Descriptions.Item label={t("admin.dataSourceTokenSummary")}>
                    <Space direction="vertical" size={2}>
                      {oauthConnection.accessTokenMasked ? (
                        <Text code>{oauthConnection.accessTokenMasked}</Text>
                      ) : null}
                      {oauthConnection.refreshTokenMasked ? (
                        <Text code>{oauthConnection.refreshTokenMasked}</Text>
                      ) : null}
                    </Space>
                  </Descriptions.Item>
                ) : null}
                {oauthConnection.grantedScopes.length > 0 ? (
                  <Descriptions.Item label={t("admin.dataSourceGrantedScopes")}>
                    <Space wrap size={[8, 8]}>
                      {oauthConnection.grantedScopes.map((scope) => (
                        <Tag key={scope}>{scope}</Tag>
                      ))}
                    </Space>
                  </Descriptions.Item>
                ) : null}
              </Descriptions>
            </div>
          ) : null}
          {oauthState === "expired" ? (
            <Alert
              showIcon
              type="warning"
              message={t("admin.dataSourceOauthExpired")}
              description={t("admin.dataSourceOauthExpiredDesc")}
            />
          ) : null}
          {oauthState === "error" ? (
            <Alert
              showIcon
              type="error"
              message={t("admin.dataSourceOauthError")}
              description={t("admin.dataSourceOauthErrorDesc")}
            />
          ) : null}
        </Card>
      );
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
          <Space>
            {wizardStep > 0 && !isEditMode ? (
              <Button disabled={saving} onClick={onPrev}>{t("admin.dataSourceWizardPrev")}</Button>
            ) : null}
            {wizardStep < 1 ? (
              <Button type="primary" disabled={saving} onClick={onNext}>
                {t("admin.dataSourceWizardNext")}
              </Button>
            ) : null}
            {wizardStep === 1 ? (
              <Button type="primary" loading={saving} onClick={onSave}>
                {t("admin.dataSourceSaveConfig")}
              </Button>
            ) : null}
          </Space>
        </div>
      }
    >
      {!isEditMode ? (
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
        {wizardStep === 0 ? (
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
                          { required: true, message: t("admin.dataSourceAccessPathRequired") },
                        ]}
                      >
                        <Input
                          disabled={isEditMode}
                          placeholder="/mnt/team-share/ops-docs"
                          onChange={isEditMode ? undefined : onInvalidateConnection}
                        />
                      </Form.Item>
                    ) : (
                      <>
                        <Form.Item
                          label={t("admin.dataSourceFeishuTargetType")}
                          name="targetType"
                          rules={[
                            {
                              required: true,
                              message: t("admin.dataSourceFeishuTargetTypeRequired"),
                            },
                          ]}
                        >
                          <Select
                            disabled={isEditMode}
                            options={[
                              {
                                label: t("admin.dataSourceFeishuTargetTypeWiki"),
                                value: "wiki_space",
                              },
                              {
                                label: t("admin.dataSourceFeishuTargetTypeDrive"),
                                value: "drive_folder",
                              },
                            ]}
                          />
                        </Form.Item>
                        <Form.Item
                          label={t("admin.dataSourceFeishuSpace")}
                          name="target"
                          rules={[
                            {
                              required: true,
                              message: t("admin.dataSourceFeishuSpaceRequired"),
                            },
                          ]}
                        >
                          <Input
                            disabled={isEditMode}
                            placeholder={
                              feishuTargetType === "drive_folder"
                                ? t("admin.dataSourceFeishuTargetPlaceholderDrive")
                                : t("admin.dataSourceFeishuTargetPlaceholderWiki")
                            }
                            onChange={isEditMode ? undefined : onInvalidateConnection}
                          />
                        </Form.Item>
                      </>
                    )}

                    {renderConnectionSection()}
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
                          <Col xs={24} md={12}>
                            <Form.Item
                              label={t("admin.dataSourceScheduleCycle")}
                              name="scheduleCycle"
                            >
                              <Select
                                options={[
                                  {
                                    label: t("admin.dataSourceCycleDaily"),
                                    value: "daily",
                                  },
                                  {
                                    label: t("admin.dataSourceCycleTwoDays"),
                                    value: "twoDays",
                                  },
                                  {
                                    label: t("admin.dataSourceCycleWeekly"),
                                    value: "weekly",
                                  },
                                ]}
                              />
                            </Form.Item>
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
