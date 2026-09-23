import { Dropdown, Tooltip } from "antd";
import type { MenuProps } from "antd";
import type { ReactNode } from "react";
import {
  ApartmentOutlined,
  BellOutlined,
  CheckOutlined,
  ClockCircleOutlined,
  CloseOutlined,
  ClusterOutlined,
  DownOutlined,
  EditOutlined,
  ExclamationCircleOutlined,
  GlobalOutlined,
  LoadingOutlined,
  PlusOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import { isDesktopRuntime } from "@/runtime/mode";
import type { SkillCreateSource } from "../MemoryDraftModal";
import type { SkillOrganizeDepth, SkillOrganizeTaskStatus } from "../../skillApi";
import type { SkillViewMode } from "../../shared";
import {
  formatSkillOrganizeElapsed,
  skillOrganizeRunningTitleKey,
} from "./skillOrganizeProgress";

export type SkillOrganizeStatus = "idle" | "running" | "success" | "skipped" | "error";

interface SkillManagementToolbarProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  skillView: SkillViewMode | "workflows";
  onSkillViewChange: (view: SkillViewMode | "workflows") => void;
  installedCount: number;
  onCreateSkill: (source: SkillCreateSource) => void;
  organizeMode: boolean;
  organizeDisabled: boolean;
  organizeDisabledReason?: string;
  organizeStatus: SkillOrganizeStatus;
  organizeRunStatus?: SkillOrganizeTaskStatus | "";
  organizeElapsedMs?: number;
  onOrganizeSkills: (mode: SkillOrganizeDepth) => void;
  onOrganizeCancel?: () => void;
  manualSkillReviewCount: number;
  manualSkillReviewDisabled: boolean;
  manualSkillReviewDisabledReason?: string;
  onSkillReviewClick: () => void;
  messageCenterCount: number;
  onMessageCenterClick: () => void;
  showMessageCenter: boolean;
  isAdmin: boolean;
  marketFilters?: ReactNode;
  onAdminPublish?: () => void;
  onNewWorkflow?: () => void;
  pendingDraftCount?: number;
  onReviewDrafts?: () => void;
  workflowSourceMode?: "local" | "cloud";
  onWorkflowSourceModeChange?: (mode: "local" | "cloud") => void;
}

function InsightCount({ count }: { count: number }) {
  if (count <= 0) {
    return null;
  }
  return <span className="memory-skill-insight-card__count">{count}</span>;
}

export default function SkillManagementToolbar({
  t,
  skillView,
  onSkillViewChange,
  onCreateSkill,
  organizeMode,
  organizeDisabled,
  organizeDisabledReason,
  organizeStatus,
  organizeRunStatus = "",
  organizeElapsedMs = 0,
  onOrganizeSkills,
  onOrganizeCancel,
  manualSkillReviewCount,
  manualSkillReviewDisabled,
  manualSkillReviewDisabledReason,
  onSkillReviewClick,
  messageCenterCount,
  onMessageCenterClick,
  showMessageCenter,
  isAdmin,
  marketFilters,
  onAdminPublish,
  onNewWorkflow,
  pendingDraftCount = 0,
  onReviewDrafts,
  workflowSourceMode = "local",
  onWorkflowSourceModeChange,
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

  const organizeMenuItems: MenuProps["items"] = [
    {
      key: "light",
      label: (
        <div className="memory-skill-create-option">
          <span className="memory-skill-create-option__icon is-upload">
            <EditOutlined />
          </span>
          <span className="memory-skill-create-option__copy">
            <strong>{t("admin.memorySkillOrganizeLight")}</strong>
            <span>{t("admin.memorySkillOrganizeLightScope")}</span>
          </span>
        </div>
      ),
    },
    {
      key: "deep",
      label: (
        <div className="memory-skill-create-option">
          <span className="memory-skill-create-option__icon is-import">
            <ClusterOutlined />
          </span>
          <span className="memory-skill-create-option__copy">
            <strong>{t("admin.memorySkillOrganizeDeep")}</strong>
            <span>{t("admin.memorySkillOrganizeDeepScope")}</span>
          </span>
        </div>
      ),
    },
  ];

  const handleOrganizeMenuClick: MenuProps["onClick"] = ({ key }) => {
    onOrganizeSkills(key as SkillOrganizeDepth);
  };

  const organizeStatusTitle = {
    idle: t("admin.memorySkillOrganizeTitle"),
    running: t(skillOrganizeRunningTitleKey(organizeRunStatus)),
    success: t("admin.memorySkillOrganizeCompleted"),
    skipped: t("admin.memorySkillOrganizeSkipped"),
    error: t("admin.memorySkillOrganizeFailed"),
  }[organizeStatus];
  const organizeElapsedHint =
    organizeStatus === "running" && organizeElapsedMs > 0
      ? formatSkillOrganizeElapsed(organizeElapsedMs, t)
      : "";
  const organizeProgressHint = [organizeStatusTitle, organizeElapsedHint]
    .filter(Boolean)
    .join(" · ");
  const organizeTooltip =
    organizeStatus === "running"
      ? organizeProgressHint
      : organizeDisabledReason ??
        (organizeStatus === "idle"
          ? t("admin.memorySkillOrganizeHint")
          : organizeStatusTitle);

  const organizeStatusIcon = {
    idle: <ApartmentOutlined />,
    running: <LoadingOutlined spin />,
    success: <CheckOutlined />,
    skipped: <ApartmentOutlined />,
    error: <ExclamationCircleOutlined />,
  }[organizeStatus];

  const renderInstalledActions = () => (
    <>
      {onReviewDrafts ? (
        <button type="button" className="memory-skill-review-drafts" onClick={onReviewDrafts}>
          {t("admin.memorySkillPendingDrafts")}
          <span className="memory-skill-review-drafts__count">{pendingDraftCount}</span>
        </button>
      ) : null}
      <Dropdown
        menu={{ items: createMenuItems, onClick: handleCreateMenuClick }}
        trigger={["click"]}
        placement="bottomRight"
        overlayClassName="memory-skill-create-dropdown"
      >
        <button
          type="button"
          className="memory-skill-create-split is-single"
          aria-haspopup="menu"
        >
          <span className="memory-skill-create-split__main">
            <PlusOutlined />
            {t("admin.memorySkillCreateButton")}
            <DownOutlined />
          </span>
        </button>
      </Dropdown>

      <span className={`memory-skill-insight-card-wrap${organizeMode && onOrganizeCancel ? " is-cancellable" : ""}`}>
        <Tooltip title={organizeTooltip} trigger={["hover", "focus"]}>
          <span
            className="memory-skill-insight-card-tooltip"
            tabIndex={organizeDisabled && organizeTooltip ? 0 : undefined}
            aria-label={organizeTooltip}
          >
            <Dropdown
              menu={{ items: organizeMenuItems, onClick: handleOrganizeMenuClick }}
              trigger={["click"]}
              placement="bottomRight"
              overlayClassName="memory-skill-create-dropdown"
              disabled={organizeDisabled}
            >
              <button
                type="button"
                className={`memory-skill-insight-card is-organize is-${organizeStatus} ${organizeMode ? "is-active" : ""}`}
                disabled={organizeDisabled}
                aria-haspopup="menu"
                aria-pressed={organizeMode}
                aria-busy={organizeStatus === "running"}
                title={organizeTooltip}
              >
                <span className="memory-skill-insight-card__icon" aria-hidden="true">
                  {organizeStatusIcon}
                </span>
                <span className="memory-skill-insight-card__title" aria-live="polite">
                  {organizeStatusTitle}
                </span>
                {organizeStatus !== "running" ? (
                  <DownOutlined className="memory-skill-insight-card__caret" aria-hidden="true" />
                ) : null}
              </button>
            </Dropdown>
          </span>
        </Tooltip>
        {organizeMode && onOrganizeCancel ? (
          <button
            type="button"
            className="memory-skill-insight-card__dismiss"
            aria-label={t("admin.memorySkillOrganizeCancel")}
            title={t("admin.memorySkillOrganizeCancel")}
            onClick={(event) => {
              event.preventDefault();
              event.stopPropagation();
              onOrganizeCancel();
            }}
          >
            <CloseOutlined aria-hidden="true" />
          </button>
        ) : null}
      </span>

      <Tooltip title={manualSkillReviewDisabledReason} trigger={["hover", "focus"]}>
        <span
          className="memory-skill-insight-card-tooltip"
          tabIndex={manualSkillReviewDisabled ? 0 : undefined}
          aria-label={manualSkillReviewDisabledReason}
        >
          <button
            type="button"
            className="memory-skill-insight-card is-review"
            onClick={onSkillReviewClick}
            disabled={manualSkillReviewDisabled}
            title={manualSkillReviewDisabled ? undefined : t("admin.memorySkillReviewCardHint")}
          >
            <span className="memory-skill-insight-card__icon">
              <ClockCircleOutlined />
              <InsightCount count={manualSkillReviewCount} />
            </span>
            <span className="memory-skill-insight-card__title">
              {t("admin.memorySkillReviewCardTitle")}
            </span>
          </button>
        </span>
      </Tooltip>

      {showMessageCenter ? (
        <button
          type="button"
          className="memory-skill-insight-card is-message"
          onClick={onMessageCenterClick}
          title={t("admin.memorySkillMessageCenterHint")}
        >
          <span className="memory-skill-insight-card__icon">
            <BellOutlined />
            <InsightCount count={messageCenterCount} />
          </span>
          <span className="memory-skill-insight-card__title">
            {t("admin.memorySkillMessageCenterTitle")}
          </span>
        </button>
      ) : null}
    </>
  );

  const renderViewActions = () => {
    if (skillView === "installed") {
      return renderInstalledActions();
    }

    if (skillView === "market" && isAdmin && !isDesktopRuntime()) {
      return (
        <>
          {marketFilters}
          <button type="button" className="memory-skill-market-publish" onClick={onAdminPublish}>
            {t("admin.memorySkillAdminPublishButton")}
          </button>
        </>
      );
    }

    if (skillView === "market") {
      return marketFilters;
    }

    if (skillView === "workflows") {
      return (
        <button type="button" className="memory-skill-create-split is-single" onClick={onNewWorkflow}>
          <span className="memory-skill-create-split__main">
            <PlusOutlined />
            {t("admin.memoryWorkflowNewButton")}
          </span>
        </button>
      );
    }

    return null;
  };

  return (
    <div className="memory-skill-toolbar">
      <div className="memory-skill-heading-group">
        <h2>{t(skillView === "market" ? "admin.memorySkillViewMarket" : skillView === "workflows" ? "admin.memorySkillViewWorkflows" : "admin.memorySkillViewInstalled")}</h2>
        {skillView !== "market" && !isDesktopRuntime() ? (
          <div className="memory-skill-location-tabs" role="tablist" aria-label={t("admin.memorySkillResourceLocation")}>
            {(["local", "cloud"] as const).map((location) => {
              const active = (skillView === "workflows" ? workflowSourceMode : skillView === "cloud" ? "cloud" : "local") === location;
              return <button type="button" role="tab" key={location} aria-selected={active}
                className={active ? "is-active" : ""}
                onClick={() => skillView === "workflows" ? onWorkflowSourceModeChange?.(location) : onSkillViewChange(location === "local" ? "installed" : "cloud")}>
                {t(location === "local" ? "admin.memorySkillLocationLocal" : "admin.memorySkillLocationCloud")}
              </button>;
            })}
          </div>
        ) : null}
      </div>

      <div className="memory-skill-toolbar-actions">{renderViewActions()}</div>
    </div>
  );
}
