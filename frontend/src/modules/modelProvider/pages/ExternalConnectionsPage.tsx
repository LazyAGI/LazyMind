import { useCallback, useEffect, useState } from "react";
import { ArrowRightOutlined, MailOutlined } from "@ant-design/icons";
import { Skeleton, Tag } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
import { unwrapApiData } from "@/modules/dataSource/api/unwrap";
import { EXTERNAL_CONNECTIONS_MAIL_PATH } from "../utils/externalConnectionUrls";
import "@/modules/modelProvider/index.scss";

const MAIL_PROVIDERS = ["gmailimap", "qqmail", "qqexmail", "netease163", "neteaseqiye"] as const;

export default function ExternalConnectionsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(true);
  const [accounts, setAccounts] = useState<string[]>([]);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const names: string[] = [];
      for (const provider of MAIL_PROVIDERS) {
        const response = await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider,
          status: "ACTIVE",
        });
        const data = unwrapApiData<any>(response.data);
        for (const item of data?.items || []) {
          const name = String(item.display_name || item.client_id || "").trim();
          if (name) {
            names.push(name);
          }
        }
      }
      setAccounts(names);
    } catch {
      setAccounts([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const connected = accounts.length > 0;

  return (
    <div className="model-provider-page-content model-provider-service-page model-provider-cloud-doc-hub">
      <header className="model-provider-cloud-doc-page-header">
        <div className="model-provider-cloud-doc-page-heading">
          <h1>{t("modelProvider.externalConnections.title")}</h1>
          <p>{t("modelProvider.externalConnections.subtitle")}</p>
        </div>
      </header>
      <div className="model-provider-cloud-doc-grid">
        {loading ? (
          <div className="model-provider-cloud-doc-skeleton">
            <Skeleton active avatar={{ shape: "square", size: 44 }} paragraph={{ rows: 2 }} />
          </div>
        ) : (
          <div className="model-provider-cloud-doc-resource-row">
            <span className="model-provider-cloud-doc-resource-logo">
              <MailOutlined />
            </span>
            <div className="model-provider-cloud-doc-resource-copy">
              <h3>{t("modelProvider.mail.title")}</h3>
              <p>
                {connected
                  ? t("modelProvider.mail.connectedHint", { account: accounts.join("、") })
                  : t("modelProvider.mail.hubHint")}
              </p>
            </div>
            <Tag
              className="model-provider-cloud-doc-resource-status"
              color={connected ? "success" : "default"}
            >
              {connected
                ? t("modelProvider.externalConnections.authValid")
                : t("modelProvider.externalConnections.credentialMissing")}
            </Tag>
            <div className="model-provider-cloud-doc-resource-controls">
              <button
                type="button"
                className="model-provider-cloud-doc-resource-action"
                onClick={() => navigate(EXTERNAL_CONNECTIONS_MAIL_PATH)}
              >
                {connected
                  ? t("modelProvider.externalConnections.manage")
                  : t("modelProvider.externalConnections.configure")}
                <ArrowRightOutlined />
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
