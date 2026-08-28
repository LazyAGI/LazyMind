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

export default function CaseCard({ item, onTry }: CaseCardProps) {
  const { t } = useTranslation();
  const coverClass = COVER_CLASS_BY_OUTPUT_TYPE[item.output_type] || "report";
  const entryType = showcaseEntryType(item.type);
  const technologyType = showcaseTechnologyType(item.type);
  const entryTypeLabel = t(`showcase.filters.capability.${entryType}`);
  const technologyTypeLabel = t(`showcase.filters.technology.${technologyType}`);

  return (
    <article className="showcase-card">
      <Link
        className="showcase-card-link"
        to={buildShowcaseLaunchPath(item.id, item.type)}
        onClick={(event) => {
          if (onTry) {
            event.preventDefault();
            onTry(item);
          }
        }}
      >
        <div className={`showcase-card-image-wrap showcase-card-cover-${coverClass}`}>
          <div className="showcase-card-image-stage">
            <img
              className="showcase-card-image"
              src={item.image_url}
              alt={t("showcase.resultPreviewAlt", { title: item.title })}
              loading="lazy"
            />
          </div>
        </div>
        <div className="showcase-card-body">
          <div className="showcase-card-category">{item.category}</div>
          <div className="showcase-card-output">{item.output_label}</div>
          <div className="showcase-card-title-row">
            <h3>{item.title}</h3>
            <span
              className={`showcase-capability-icon is-${entryType}`}
              role="img"
              aria-label={entryTypeLabel}
            >
              {entryType === "chat"
                ? <MessageOutlined aria-hidden="true" />
                : <ScheduleOutlined aria-hidden="true" />}
            </span>
          </div>
          <div className="showcase-card-tags" aria-label={t("showcase.cardTagsLabel")}>
            <span>{item.category}</span>
            <span>{entryTypeLabel}</span>
            <span>{technologyTypeLabel}</span>
          </div>
          <p>{item.description}</p>
        </div>
      </Link>
      <div className="showcase-card-footer">
        <span className="showcase-card-result">{item.result_summary}</span>
        <Link
          className="showcase-detail-link"
          to={`/agent/chat/cases/${encodeURIComponent(item.id)}`}
        >
          {t("showcase.viewDetail")}
          <ArrowRightOutlined aria-hidden="true" />
        </Link>
      </div>
    </article>
  );
}
