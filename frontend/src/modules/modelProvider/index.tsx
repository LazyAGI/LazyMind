import { useState } from "react";
import { Input } from "antd";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { ApiOutlined, AppstoreOutlined, ControlOutlined, SearchOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import "./index.scss";

const modelProviderTabs = [
  {
    key: "/model-providers/models",
    labelKey: "modelProvider.tabs.models",
    icon: <ApiOutlined />,
  },
  {
    key: "/model-providers/external-services",
    labelKey: "modelProvider.tabs.externalServices",
    icon: <AppstoreOutlined />,
  },
  {
    key: "/model-providers/default-services",
    labelKey: "modelProvider.tabs.defaultServices",
    icon: <ControlOutlined />,
  },
];

export default function ModelProviderLayout() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const [externalServiceSearchValue, setExternalServiceSearchValue] = useState("");
  const isExternalServicesPage = location.pathname.startsWith("/model-providers/external-services");

  return (
    <main className="model-provider-page">
      <div className="model-provider-layout-frame">
        <nav className="model-provider-tabs" aria-label={t("modelProvider.tabs.aria")}>
          {modelProviderTabs.map((item) => {
            const active = location.pathname.startsWith(item.key);
            return (
              <button
                className={`model-provider-tab${active ? " is-active" : ""}`}
                key={item.key}
                type="button"
                onClick={() => navigate(item.key)}
              >
                {item.icon}
                <span>{t(item.labelKey)}</span>
              </button>
            );
          })}
          {isExternalServicesPage ? (
            <Input
              allowClear
              className="model-provider-tabs-search"
              prefix={<SearchOutlined />}
              value={externalServiceSearchValue}
              onChange={(event) => setExternalServiceSearchValue(event.target.value)}
              placeholder={t("modelProvider.external.searchPlaceholder")}
            />
          ) : null}
        </nav>
        <Outlet context={{ externalServiceSearchValue }} />
      </div>
    </main>
  );
}
