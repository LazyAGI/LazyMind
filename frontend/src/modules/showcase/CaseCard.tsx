import type { MouseEvent } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { ArrowRightOutlined, MessageOutlined, ScheduleOutlined } from "@ant-design/icons";
import {
  buildShowcaseLaunchPath,
  showcaseEntryType,
  showcaseTechnologyType,
} from "./classification";
import type { ShowcaseCase } from "./api";

interface CaseCardProps {
  item: ShowcaseCase;
  onTry?: (item: ShowcaseCase) => void;
  primaryAction?: "try" | "details";
  showWorkflowHot?: boolean;
}

const COVER_CLASS_BY_OUTPUT_TYPE: Record<string, string> = {
  report: "report",
  dashboard: "dashboard",
  slides: "slides",
  document: "document",
  images: "image",
  web: "web",
  meeting: "meeting",
  table: "table",
};

export default function CaseCard({
  item,
  onTry,
  primaryAction = "try",
  showWorkflowHot = false,
}: CaseCardProps) {
  const { t } = useTranslation();
  const coverClass = COVER_CLASS_BY_OUTPUT_TYPE[item.output_type] || "report";
  const entryType = showcaseEntryType(item.type);
  const technologyType = showcaseTechnologyType(item.type);
  const entryTypeLabel = t(`showcase.filters.capability.${entryType}`);
  const technologyTypeLabel = t(`showcase.filters.technology.${technologyType}`);
  const detailPath = `/agent/chat/cases/${encodeURIComponent(item.id)}`;
  const launchPath = buildShowcaseLaunchPath(item.id, item.type);
  const isDetailPrimary = primaryAction === "details";
  const primaryPath = isDetailPrimary ? detailPath : launchPath;
  const secondaryPath = isDetailPrimary ? launchPath : detailPath;
  const handleTry = (event: MouseEvent<HTMLAnchorElement>) => {
    if (!onTry) return;
    event.preventDefault();
    onTry(item);
  };

  return (
    <article className="showcase-card">
      <Link
        className="showcase-card-image-link"
        aria-label={t(isDetailPrimary ? "showcase.viewDetail" : "showcase.try")}
        to={primaryPath}
        onClick={isDetailPrimary ? undefined : handleTry}
      >
        <div className={`showcase-card-image-wrap showcase-card-cover-${coverClass}`}>
          <div className="showcase-card-image-stage">
            <img
              className="showcase-card-image"
              src={item.image_url}
              alt=""
              loading="lazy"
            />
          </div>
        </div>
      </Link>
      <div className="showcase-card-body">
        <div className="showcase-card-category">{item.category}</div>
        <div className="showcase-card-output">{item.output_label}</div>
        <div className="showcase-card-title-row">
          <Link
            className="showcase-card-title-link"
            to={primaryPath}
            onClick={isDetailPrimary ? undefined : handleTry}
          >
            <h3>{item.title}</h3>
            {showWorkflowHot && technologyType === "workflow" ? (
              <span className="showcase-workflow-hot">
                {t("showcase.workflowHotBadge")}
              </span>
            ) : null}
            <span
              className={`showcase-capability-icon is-${entryType}`}
              role="img"
              aria-label={entryTypeLabel}
            >
              {entryType === "chat"
                ? <MessageOutlined aria-hidden="true" />
                : <ScheduleOutlined aria-hidden="true" />}
            </span>
          </Link>
          <Link
            className="showcase-detail-link"
            to={secondaryPath}
            onClick={isDetailPrimary ? handleTry : undefined}
          >
            {t(isDetailPrimary ? "showcase.experienceNow" : "showcase.viewDetail")}
            <ArrowRightOutlined aria-hidden="true" />
          </Link>
        </div>
        <div className="showcase-card-tags" aria-label={t("showcase.cardTagsLabel")}>
          <span>{item.category}</span>
          <span>{entryTypeLabel}</span>
          <span>{technologyTypeLabel}</span>
        </div>
        <p>{item.description}</p>
      </div>
    </article>
  );
}
