import { useMemo } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import CaseCard from "./CaseCard";
import { matchesShowcaseEntryType, type ShowcaseCase, type ShowcaseEntryType } from "./api";
import "./index.scss";

interface FeaturedCasesProps {
  type: ShowcaseEntryType;
  items: ShowcaseCase[];
  isLoading: boolean;
  onTry?: (item: ShowcaseCase) => void;
}

const FEATURED_HOME_LIMIT = 8;

export default function FeaturedCases({ type, items, isLoading, onTry }: FeaturedCasesProps) {
  const { t } = useTranslation();

  const featuredItems = useMemo(() => {
    return items.filter(
      (item) => item.featured && matchesShowcaseEntryType(item.type, type),
    ).slice(0, FEATURED_HOME_LIMIT);
  }, [items, type]);

  if (!isLoading && featuredItems.length === 0) {
    return null;
  }

  return (
    <section className="showcase-featured" aria-labelledby="showcase-featured-title">
      <div className="showcase-featured-heading">
        <h2 id="showcase-featured-title">{t("showcase.featuredTitle")}</h2>
        <Link className="showcase-more-link" to={`/agent/chat/cases?type=${type}`}>
          {t("showcase.viewMore")} <span aria-hidden="true">→</span>
        </Link>
      </div>
      {isLoading ? (
        <div className="showcase-loading" role="status">
          {t("showcase.loadingFeatured")}
        </div>
      ) : (
        <div className="showcase-grid showcase-featured-grid">
          {featuredItems.map((item) => (
            <CaseCard key={item.id} item={item} onTry={onTry} />
          ))}
        </div>
      )}
    </section>
  );
}
