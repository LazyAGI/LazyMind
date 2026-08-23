import { useEffect, useMemo, useState } from "react";
import { ArrowLeftOutlined, SearchOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router-dom";
import CaseCard from "./CaseCard";
import { listShowcaseCases, type ShowcaseCase } from "./api";
import "./index.scss";

export default function GalleryPage() {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage || i18n.language;
  const [searchParams] = useSearchParams();
  const requestedType = searchParams.get("type");
  const type = requestedType === "chat" || requestedType === "work" ? requestedType : "";
  const [items, setItems] = useState<ShowcaseCase[]>([]);
  const [categories, setCategories] = useState<string[]>([]);
  const [keyword, setKeyword] = useState("");
  const [category, setCategory] = useState("");
  const [isLoading, setIsLoading] = useState(true);
  const [hasError, setHasError] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    listShowcaseCases({}, { signal: controller.signal })
      .then((response) => {
        setItems((response.cases || []).filter((item) => item.gallery && (!type || item.type === type)));
        setCategories(response.categories);
        setCategory((current) =>
          response.categories.includes(current)
            ? current
            : response.categories[0] || "",
        );
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setHasError(true);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setIsLoading(false);
        }
      });
    return () => controller.abort();
  }, [locale, type]);

  const filteredItems = useMemo(() => {
    const normalizedKeyword = keyword.trim().toLowerCase();
    return items.filter((item) => {
      const matchesCategory =
        category === "" ||
        category === categories[0] ||
        item.category === category;
      const searchable = [
        item.title,
        item.description,
        item.category,
        item.prompt_short,
      ]
        .join(" ")
        .toLowerCase();
      return matchesCategory
        && (!normalizedKeyword || searchable.includes(normalizedKeyword));
    });
  }, [category, categories, items, keyword]);

  return (
    <main className="showcase-page showcase-gallery-page">
      <Link className="showcase-back-link" to="/agent/chat/home">
        <ArrowLeftOutlined aria-hidden="true" />
        {t("showcase.backToHome")}
      </Link>
      <header className="showcase-page-header">
        <h1>{t("showcase.galleryTitle")}</h1>
        <p>{t("showcase.galleryDescription")}</p>
      </header>

      <div className="showcase-toolbar">
        <label className="showcase-search">
          <SearchOutlined className="showcase-search-icon" aria-hidden="true" />
          <span className="sr-only">{t("showcase.searchLabel")}</span>
          <input
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            placeholder={t("showcase.searchPlaceholder")}
          />
        </label>
        <div className="showcase-category-list" aria-label={t("showcase.categoryLabel")}>
          {categories.map((item) => (
            <button
              className={item === category ? "is-active" : ""}
              key={item}
              type="button"
              aria-pressed={item === category}
              onClick={() => setCategory(item)}
            >
              {item}
            </button>
          ))}
        </div>
      </div>

      {isLoading ? (
        <div className="showcase-empty" role="status">{t("showcase.loading")}</div>
      ) : hasError ? (
        <div className="showcase-empty" role="alert">{t("showcase.loadError")}</div>
      ) : filteredItems.length === 0 ? (
        <div className="showcase-empty">
          <strong>{t("showcase.noMatches")}</strong>
          <span>{t("showcase.noMatchesHint")}</span>
        </div>
      ) : (
        <div className="showcase-grid showcase-gallery-grid">
          {filteredItems.map((item) => (
            <CaseCard key={item.id} item={item} />
          ))}
        </div>
      )}
    </main>
  );
}
