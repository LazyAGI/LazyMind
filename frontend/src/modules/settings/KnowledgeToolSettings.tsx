import type { RefObject } from "react";
import { Button } from "antd";
import { ArrowLeftOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import ToolManagementSection from "@/modules/modelProvider/components/ToolManagementSection";
import ExternalServicesPage from "@/modules/modelProvider/pages/ExternalServicesPage";

export const knowledgeToolViews = ["web-search", "academic-search", "wikipedia"] as const;
export type KnowledgeToolView = typeof knowledgeToolViews[number];

export function isKnowledgeToolView(value: string | null): value is KnowledgeToolView {
  return knowledgeToolViews.some((view) => view === value);
}

interface KnowledgeToolSettingsProps {
  headingRef: RefObject<HTMLHeadingElement>;
  onBack: () => void;
  view: KnowledgeToolView;
}

export default function KnowledgeToolSettings({ headingRef, onBack, view }: KnowledgeToolSettingsProps) {
  const { t } = useTranslation();
  const copyKey = view === "web-search"
    ? "settingsPage.knowledge.groups.search.webSearch"
    : view === "academic-search"
      ? "settingsPage.knowledge.groups.search.academicSearch"
      : "settingsPage.knowledge.groups.search.wikipedia";
  const title = t(`${copyKey}.name`);
  const description = t(`${copyKey}.description`);

  return (
    <section className="settings-knowledge-tool-settings">
      <header className="settings-detail-header settings-knowledge-tool-header">
        <div>
          <Button className="settings-knowledge-tool-back" icon={<ArrowLeftOutlined />} type="text" onClick={onBack}>
            {t("common.back")}
          </Button>
          <h1 ref={headingRef} tabIndex={-1}>{title}</h1>
          <p>{description}</p>
        </div>
      </header>
      <div className="settings-knowledge-tool-surface">
        {view === "wikipedia" ? (
          <ToolManagementSection
            initialQuery="Wikipedia"
            title={t("settingsPage.systemTools.builtinTitle")}
            view="builtin"
          />
        ) : (
          <ExternalServicesPage
            includeBuiltinTools={false}
            includeDependencies={false}
            includeMcp={false}
            visibleCategories={view === "web-search" ? ["search"] : ["academic"]}
          />
        )}
      </div>
    </section>
  );
}
