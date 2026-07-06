import { ArrowRightOutlined, DatabaseOutlined } from "@ant-design/icons";
import { Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

export default function DatabaseProviderPanel() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  return (
    <section className="model-provider-service-category">
      <div className="model-provider-service-category-top">
        <div className="model-provider-service-category-head">
          <span>
            <DatabaseOutlined />
          </span>
          <div>
            <h3>{t("admin.dataSourceDatabaseSectionTitle")}</h3>
            <p>{t("admin.dataSourceDatabaseSubtitle")}</p>
          </div>
        </div>
      </div>
      <div className="model-provider-cloud-doc-grid">
        <button
          type="button"
          className="model-provider-service-card"
          onClick={() => navigate("/data-sources/database-connections")}
        >
          <span className="model-provider-service-logo model-provider-service-logo-blue">
            <DatabaseOutlined className="model-provider-service-logo-icon" />
          </span>
          <div className="model-provider-service-card-copy">
            <div>
              <div className="model-provider-service-title-row">
                <h4>{t("admin.dataSourceDatabaseTitle")}</h4>
              </div>
              <Tooltip placement="topLeft" title={t("admin.dataSourceDatabaseProviderDesc")}>
                <span className="model-provider-service-summary-wrap">
                  <p className="model-provider-service-summary">
                    {t("admin.dataSourceDatabaseProviderDesc")}
                  </p>
                </span>
              </Tooltip>
            </div>
          </div>
          <span className="model-provider-service-card-arrow" aria-hidden="true">
            <ArrowRightOutlined />
          </span>
        </button>
      </div>
    </section>
  );
}
