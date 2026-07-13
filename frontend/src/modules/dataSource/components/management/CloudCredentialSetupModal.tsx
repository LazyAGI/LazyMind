import { Alert, Form, Input, Modal } from "antd";
import type { FormInstance } from "antd/es/form";
import type { TFunction } from "i18next";
import { FeishuCredentialHintAlertFromForm } from "@/modules/dataSource/common/FeishuCredentialHintAlert";
import type { FeishuAccountFormValues } from "@/modules/dataSource/common/feishuAccounts";
import type { CloudDataSourceProvider } from "@/modules/dataSource/common/feishuOAuth";
import {
  CLOUD_DOCUMENTS_FEISHU_SETUP_PATH,
  CLOUD_DOCUMENTS_NOTION_SETUP_PATH,
} from "@/modules/modelProvider/utils/cloudDocumentUrls";

interface CloudCredentialSetupModalProps {
  t: TFunction;
  cloudSetupProvider: CloudDataSourceProvider;
  feishuSetupForm: FormInstance<FeishuAccountFormValues>;
  open: boolean;
  submitting: boolean;
  onCancel: () => void;
  onSave: () => void;
}

export default function CloudCredentialSetupModal({
  t,
  cloudSetupProvider,
  feishuSetupForm,
  open,
  submitting,
  onCancel,
  onSave,
}: CloudCredentialSetupModalProps) {
  const isFeishu = cloudSetupProvider === "feishu";

  return (
    <Modal
      title={
        isFeishu
          ? t("admin.dataSourceFeishuCredentialModalTitle")
          : t("admin.dataSourceNotionCredentialModalTitle")
      }
      open={open}
      destroyOnHidden
      onCancel={onCancel}
      onOk={onSave}
      okText={
        isFeishu
          ? t("admin.dataSourceFeishuCredentialSaveAndSelect")
          : t("admin.dataSourceNotionCredentialSaveAndSelect")
      }
      okButtonProps={{ loading: submitting }}
      cancelButtonProps={{ disabled: submitting }}
      cancelText={t("common.cancel")}
    >
      <Form form={feishuSetupForm} layout="vertical">
        {isFeishu ? (
          <Form.Item
            label={t("admin.dataSourceFeishuAccountName")}
            name="name"
          >
            <Input
              placeholder={t("admin.dataSourceFeishuAccountNamePlaceholder")}
            />
          </Form.Item>
        ) : null}
        <Form.Item
          label={t("admin.dataSourceAppId")}
          name="appId"
          rules={[
            {
              required: true,
              message: t("admin.dataSourceAppIdRequired"),
            },
          ]}
        >
          <Input placeholder={t("admin.dataSourceAppIdPlaceholder")} />
        </Form.Item>
        <Form.Item
          label={t("admin.dataSourceAppSecret")}
          name="appSecret"
          rules={[
            {
              required: true,
              message: t("admin.dataSourceAppSecretRequired"),
            },
          ]}
        >
          <Input.Password placeholder={t("admin.dataSourceAppSecretPlaceholder")} />
        </Form.Item>
        {isFeishu ? (
          <FeishuCredentialHintAlertFromForm form={feishuSetupForm} />
        ) : (
          <Alert
            showIcon
            type="info"
            message={t("admin.dataSourceNotionCredentialHint")}
          />
        )}
        {isFeishu ? (
          <p style={{ marginTop: 12, marginBottom: 0 }}>
            <a href={CLOUD_DOCUMENTS_FEISHU_SETUP_PATH} target="_blank" rel="noreferrer">
              {t("admin.dataSourceFeishuSetupGuideAction")}
            </a>
          </p>
        ) : (
          <p style={{ marginTop: 12, marginBottom: 0 }}>
            <a
              href={`${CLOUD_DOCUMENTS_NOTION_SETUP_PATH}?from=create-source`}
              target="_blank"
              rel="noreferrer"
            >
              {t("admin.dataSourceNotionSetupGuideAction")}
            </a>
            {t("admin.dataSourceNotionSetupGuideHint")}
          </p>
        )}
      </Form>
    </Modal>
  );
}
