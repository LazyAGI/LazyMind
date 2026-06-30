import { Alert, Form, Typography } from "antd";
import type { FormInstance } from "antd/es/form";
import { useTranslation } from "react-i18next";

import { getFeishuDataSourceCallbackUrl } from "./feishuOAuth";

const { Link } = Typography;

export const FEISHU_OPEN_PLATFORM_URL = "https://open.feishu.cn/app";

export function getFeishuOpenPlatformAppUrl(appId: string) {
  return `${FEISHU_OPEN_PLATFORM_URL}/${encodeURIComponent(appId)}/baseinfo`;
}

interface FeishuCredentialHintAlertProps {
  appId?: string;
  callbackUrl?: string;
}

export function FeishuCredentialHintAlert({
  appId,
  callbackUrl = getFeishuDataSourceCallbackUrl(),
}: FeishuCredentialHintAlertProps) {
  const { t } = useTranslation();
  const normalizedAppId = `${appId || ""}`.trim();
  if (!normalizedAppId) {
    return null;
  }

  const openPlatformUrl = getFeishuOpenPlatformAppUrl(normalizedAppId);

  return (
    <Alert
      showIcon
      type="info"
      message={
        <>
          {t("admin.dataSourceFeishuCredentialHintPrefix")}{" "}
          <Link href={openPlatformUrl} target="_blank" rel="noreferrer">
            {t("admin.dataSourceFeishuOpenPlatformLinkLabel")}
          </Link>
          {t("admin.dataSourceFeishuCredentialHintSuffix", { callbackUrl })}
        </>
      }
    />
  );
}

interface FeishuCredentialHintAlertFromFormProps {
  form: FormInstance;
  fieldName?: string;
}

export function FeishuCredentialHintAlertFromForm({
  form,
  fieldName = "appId",
}: FeishuCredentialHintAlertFromFormProps) {
  const appId = Form.useWatch(fieldName, form);
  return <FeishuCredentialHintAlert appId={appId} />;
}
