import { Alert, Form, Input } from "antd";
import { FeishuCredentialHintAlertFromForm } from "@/modules/dataSource/common/FeishuCredentialHintAlert";
import type { CloudDocumentProvidersVm } from "../hooks/useCloudDocumentProviders";
import { CLOUD_DOCUMENTS_FEISHU_SETUP_PATH, CLOUD_DOCUMENTS_GITHUB_SETUP_PATH,
  CLOUD_DOCUMENTS_NOTION_SETUP_PATH } from "../utils/cloudDocumentUrls";

export default function CloudDocumentCredentialForm({ vm }: { vm: CloudDocumentProvidersVm }) {
  const { t, feishuSetupForm, cloudSetupProvider } = vm;
  return (
      <Form form={feishuSetupForm} layout="vertical">
        <Form.Item label={t("modelProvider.cloudDocuments.feishuAccountName")} name="name">
          <Input placeholder={t("modelProvider.cloudDocuments.feishuAccountNamePlaceholder")} />
        </Form.Item>
        <Form.Item
          label={t("modelProvider.cloudDocuments.appId")}
          name="appId"
          rules={[{ required: true, message: t("modelProvider.cloudDocuments.appIdRequired") }]}
        >
          <Input
            placeholder={t(
              cloudSetupProvider === "notion"
                ? "modelProvider.cloudDocuments.notionAppIdPlaceholder"
                : cloudSetupProvider === "github"
                  ? "modelProvider.cloudDocuments.githubAppIdPlaceholder"
                  : "modelProvider.cloudDocuments.appIdPlaceholder",
            )}
          />
        </Form.Item>
        <Form.Item
          label={t("modelProvider.cloudDocuments.appSecret")}
          name="appSecret"
          rules={[
            { required: true, message: t("modelProvider.cloudDocuments.appSecretRequired") },
          ]}
        >
          <Input.Password placeholder={t("modelProvider.cloudDocuments.appSecretPlaceholder")} />
        </Form.Item>
        {cloudSetupProvider === "feishu" ? (
          <FeishuCredentialHintAlertFromForm form={feishuSetupForm} />
        ) : cloudSetupProvider === "github" ? (
          <Alert
            showIcon
            type="info"
            message={t("modelProvider.cloudDocuments.githubCredentialHint")}
          />
        ) : (
          <Alert showIcon type="info" message={t("modelProvider.cloudDocuments.notionCredentialHint")} />
        )}
        {cloudSetupProvider === "github" ? (
          <p style={{ marginTop: 12, marginBottom: 0 }}>
            <a href={CLOUD_DOCUMENTS_GITHUB_SETUP_PATH} target="_blank" rel="noreferrer">
              {t("modelProvider.cloudDocuments.githubSetupGuideAction")}
            </a>
            {t("modelProvider.cloudDocuments.githubSetupGuideHint")}
          </p>
        ) : cloudSetupProvider !== "feishu" ? (
          <p style={{ marginTop: 12, marginBottom: 0 }}>
            <a
              href={`${CLOUD_DOCUMENTS_NOTION_SETUP_PATH}?from=cloud-documents`}
              target="_blank"
              rel="noreferrer"
            >
              {t("modelProvider.cloudDocuments.notionSetupGuideAction")}
            </a>
            {t("modelProvider.cloudDocuments.notionSetupGuideHint")}
          </p>
        ) : (
          <p style={{ marginTop: 12, marginBottom: 0 }}>
            <a href={CLOUD_DOCUMENTS_FEISHU_SETUP_PATH} target="_blank" rel="noreferrer">
              {t("modelProvider.cloudDocuments.feishuSetupGuideAction")}
            </a>
          </p>
        )}
      </Form>
  );
}
