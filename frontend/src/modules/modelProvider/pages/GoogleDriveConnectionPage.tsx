import { ArrowLeftOutlined, GoogleOutlined } from "@ant-design/icons";
import { Alert, Button, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import GoogleDriveConnectionSection from "@/modules/modelProvider/components/GoogleDriveConnectionSection";
import {
  getCloudDataSourceCallbackUrl,
  isGoogleOAuthRedirectUriSupported,
} from "@/modules/dataSource/oauth/urls";
import { CLOUD_DOCUMENTS_PATH } from "@/modules/modelProvider/utils/cloudDocumentUrls";
import "@/modules/modelProvider/index.scss";
import "./googleDriveConnectionPage.scss";

const { Paragraph, Text } = Typography;

export default function GoogleDriveConnectionPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const callbackUrl = getCloudDataSourceCallbackUrl("googledrive");
  const callbackUrlSupported = isGoogleOAuthRedirectUriSupported(callbackUrl);

  return (
    <div className="google-drive-provider-page">
      <header className="google-drive-provider-header">
        <Button
          type="link"
          icon={<ArrowLeftOutlined />}
          onClick={() => navigate(CLOUD_DOCUMENTS_PATH)}
        >
          {t("admin.dataSourceGoogleDriveBackProviders")}
        </Button>
        <div className="google-drive-provider-heading">
          <span aria-hidden="true"><GoogleOutlined /></span>
          <div>
            <h1>{t("admin.dataSourceGoogleDrivePageTitle")}</h1>
            <Paragraph>{t("admin.dataSourceGoogleDrivePageDesc")}</Paragraph>
          </div>
        </div>
      </header>

      <main className="google-drive-provider-content">
        <div className={`google-drive-provider-callback${callbackUrlSupported ? "" : " is-invalid"}`}>
          <Text strong>{t("admin.dataSourceGoogleDriveCallbackLabel")}</Text>
          <code>{callbackUrl}</code>
          {callbackUrlSupported ? (
            <Paragraph>{t("admin.dataSourceGoogleDriveHttpsHint")}</Paragraph>
          ) : (
            <Alert
              showIcon
              type="error"
              message={t("admin.dataSourceGoogleDriveInvalidCallbackTitle")}
              description={t("admin.dataSourceGoogleDriveInvalidCallbackHint")}
            />
          )}
        </div>
        <GoogleDriveConnectionSection />
      </main>
    </div>
  );
}
