import { Dropdown } from "antd";
import type { MenuProps } from "antd";
import {
  BellOutlined,
  ClockCircleOutlined,
  DownOutlined,
  GlobalOutlined,
  PlusOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import type { SkillCreateSource } from "../MemoryDraftModal";
import type { SkillViewMode } from "../../shared";

interface SkillManagementToolbarProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  skillView: SkillViewMode | "plugins";
  onSkillViewChange: (view: SkillViewMode | "plugins") => void;
  installedCount: number;
  trashCount?: number;
  onCreateSkill: (source: SkillCreateSource) => void;
  manualSkillReviewCount: number;
  manualSkillReviewLoading: boolean;
  manualSkillReviewRunning: boolean;
  onSkillReviewClick: () => void;
  messageCenterCount: number;
  onMessageCenterClick: () => void;
  showMessageCenter: boolean;
  isAdmin: boolean;
  onAdminPublish?: () => void;
  onNewPlugin?: () => void;
}

function InsightCount({ count }: { count: number }) {
  return <span className="memory-skill-insight-card__count">{count}</span>;
}

export default function SkillManagementToolbar({
  t,
  skillView,
  onSkillViewChange,
  installedCount,
  trashCount = 0,
  onCreateSkill,
  manualSkillReviewCount,
  manualSkillReviewLoading,
  manualSkillReviewRunning,
  onSkillReviewClick,
  messageCenterCount,
  onMessageCenterClick,
  showMessageCenter,
  isAdmin,
  onAdminPublish,
  onNewPlugin,
}: SkillManagementToolbarProps) {
  const createMenuItems: MenuProps["items"] = [
    {
      key: "zip",
      label: (
        <div className="memory-skill-create-option">
          <span className="memory-skill-create-option__icon is-upload">
            <UploadOutlined />
          </span>
          <span className="memory-skill-create-option__copy">
            <strong>{t("admin.memorySkillCreateUploadTitle")}</strong>
            <span>{t("admin.memorySkillCreateUploadDesc")}</span>
          </span>
        </div>
      ),
    },
    {
      key: "url",
      label: (
        <div className="memory-skill-create-option">
          <span className="memory-skill-create-option__icon is-import">
            <GlobalOutlined />
          </span>
          <span className="memory-skill-create-option__copy">
            <strong>{t("admin.memorySkillCreateImportTitle")}</strong>
            <span>{t("admin.memorySkillCreateImportDesc")}</span>
          </span>
        </div>
      ),
    },
  ];

  const handleCreateMenuClick: MenuProps["onClick"] = ({ key }) => {
    onCreateSkill(key as SkillCreateSource);
  };

  const renderInstalledActions = () => (
    <>
      <div className="memory-skill-create-split">
        <button
          type="button"
          className="memory-skill-create-split__main"
          onClick={() => onCreateSkill("manual")}
        >
          <PlusOutlined />
          {t("admin.memorySkillCreateButton")}
        </button>
        <Dropdown
          menu={{ items: createMenuItems, onClick: handleCreateMenuClick }}
          trigger={["click"]}
          placement="bottomRight"
          overlayClassName="memory-skill-create-dropdown"
        >
          <button
            type="button"
            className="memory-skill-create-split__trigger"
            aria-label={t("admin.memorySkillCreateButton")}
          >
            <DownOutlined />
          </button>
        </Dropdown>
      </div>

      <button
        type="button"
        className="memory-skill-insight-card is-review"
        onClick={onSkillReviewClick}
        disabled={manualSkillReviewLoading || manualSkillReviewRunning}
      >
        <span className="memory-skill-insight-card__icon">
          <ClockCircleOutlined />
          <InsightCount count={manualSkillReviewCount} />
        </span>
        <span className="memory-skill-insight-card__body">
          <span className="memory-skill-insight-card__title">
            {t("admin.memorySkillReviewCardTitle")}
          </span>
          <span className="memory-skill-insight-card__hint">
            {t("admin.memorySkillReviewCardHint")}
          </span>
        </span>
      </button>

      {showMessageCenter ? (
        <button
          type="button"
          className="memory-skill-insight-card is-message"
          onClick={onMessageCenterClick}
        >
          <span className="memory-skill-insight-card__icon">
            <BellOutlined />
            <InsightCount count={messageCenterCount} />
          </span>
          <span className="memory-skill-insight-card__body">
            <span className="memory-skill-insight-card__title">
              {t("admin.memorySkillMessageCenterTitle")}
            </span>
            <span className="memory-skill-insight-card__hint">
              {t("admin.memorySkillMessageCenterHint")}
            </span>
          </span>
        </button>
      ) : null}
    </>
  );

  const renderViewActions = () => {
    if (skillView === "installed") {
      return renderInstalledActions();
    }

    if (skillView === "market" && isAdmin) {
      return (
        <button type="button" className="memory-skill-market-publish" onClick={onAdminPublish}>
          {t("admin.memorySkillAdminPublishButton")}
        </button>
      );
    }

    if (skillView === "plugins") {
      return (
        <button type="button" className="memory-skill-create-split is-single" onClick={onNewPlugin}>
          <span className="memory-skill-create-split__main">
            <PlusOutlined />
            {t("admin.memoryPluginNewButton")}
          </span>
        </button>
      );
    }

    return null;
  };

  return (
    <div className="memory-skill-toolbar">
      <div
        className="memory-skill-view-tabs"
        role="tablist"
        aria-label={t("admin.memorySkillViewBarLabel")}
      >
        <button
          type="button"
          role="tab"
          className={`memory-skill-view-tab ${skillView === "installed" ? "is-active" : ""}`}
          aria-selected={skillView === "installed"}
          onClick={() => onSkillViewChange("installed")}
        >
          {t("admin.memorySkillViewInstalledWithCount", { count: installedCount })}
        </button>
        <button
          type="button"
          role="tab"
          className={`memory-skill-view-tab ${skillView === "market" ? "is-active" : ""}`}
          aria-selected={skillView === "market"}
          onClick={() => onSkillViewChange("market")}
        >
          {t("admin.memorySkillViewMarket")}
        </button>
        <button
          type="button"
          role="tab"
          className={`memory-skill-view-tab ${skillView === "trash" ? "is-active" : ""}`}
          aria-selected={skillView === "trash"}
          onClick={() => onSkillViewChange("trash")}
        >
          {t("admin.memorySkillViewTrashWithCount", { count: trashCount })}
        </button>
        <button
          type="button"
          role="tab"
          className={`memory-skill-view-tab ${skillView === "plugins" ? "is-active" : ""}`}
          aria-selected={skillView === "plugins"}
          onClick={() => onSkillViewChange("plugins")}
        >
          {t("admin.memorySkillViewPlugins")}
        </button>
      </div>

      <div className="memory-skill-toolbar-actions">{renderViewActions()}</div>
    </div>
  );
}
