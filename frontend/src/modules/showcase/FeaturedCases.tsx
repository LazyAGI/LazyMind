import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import CaseCard from "./CaseCard";
import { listShowcaseCases, type ShowcaseCase } from "./api";
import "./index.scss";

type FeaturedSkillType = "chat" | "work";

interface FeaturedCasesProps {
  type: FeaturedSkillType;
  onTry?: (item: ShowcaseCase) => void;
}

export default function FeaturedCases({ type, onTry }: FeaturedCasesProps) {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage || i18n.language;
  const [items, setItems] = useState<ShowcaseCase[]>([]);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    const controller = new AbortController();
    listShowcaseCases({}, { signal: controller.signal })
      .then((response) => setItems(response.cases))
      .catch(() => {
        if (!controller.signal.aborted) {
          setItems([]);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setIsLoading(false);
        }
      });
    return () => controller.abort();
  }, [locale]);

  const featuredItems = useMemo(() => {
    return items.filter((item) => item.featured && item.type === type);
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
