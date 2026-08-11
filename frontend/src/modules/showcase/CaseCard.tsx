import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { ArrowRightOutlined } from "@ant-design/icons";
import type { ShowcaseCase } from "./api";
import { translateShowcaseCategory } from "./i18n";

interface CaseCardProps {
  item: ShowcaseCase;
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

export default function CaseCard({ item }: CaseCardProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const coverClass = COVER_CLASS_BY_OUTPUT_TYPE[item.output_type] || "report";

  return (
    <article className="showcase-card">
      <Link
        className="showcase-card-link"
        to={`/agent/chat/cases/${encodeURIComponent(item.id)}`}
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
          <div className="showcase-card-category">{translateShowcaseCategory(t, item.category)}</div>
          <div className="showcase-card-output">{item.output_label}</div>
          <h3>{item.title}</h3>
          <p>{item.description}</p>
        </div>
      </Link>
      <div className="showcase-card-footer">
        <span className="showcase-card-result">{item.result_summary}</span>
        <button
          type="button"
          className="showcase-try-button"
          onClick={() =>
            navigate(
              `/agent/chat/home?showcase_case=${encodeURIComponent(item.id)}`,
            )
          }
        >
          {t("showcase.try")}
          <ArrowRightOutlined aria-hidden="true" />
        </button>
      </div>
    </article>
  );
}
