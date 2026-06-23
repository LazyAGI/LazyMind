import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { ApiOutlined, ControlOutlined, FilePdfOutlined, ToolOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import "./index.scss";

const modelProviderTabs = [
  {
    key: "/model-providers/default-services",
    labelKey: "modelProvider.tabs.defaultServices",
    icon: <ControlOutlined />,
  },
  {
    key: "/model-providers/models",
    labelKey: "modelProvider.tabs.models",
    icon: <ApiOutlined />,
  },
  {
    key: "/model-providers/document-parsing",
    labelKey: "modelProvider.tabs.documentParsing",
    icon: <FilePdfOutlined />,
  },
  {
    key: "/model-providers/tools",
    labelKey: "modelProvider.tabs.tools",
    icon: <ToolOutlined />,
  },
];

export default function ModelProviderLayout() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();

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
        </nav>
        <Outlet />
      </div>
    </main>
  );
}
