import { useEffect, useMemo, useState } from "react";
import { ArrowLeftOutlined, SearchOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import CaseCard from "./CaseCard";
import { listShowcaseCases, type ShowcaseCase } from "./api";
import {
  showcaseEntryType,
  showcaseTechnologyType,
  type ShowcaseEntryType,
  type ShowcaseTechnologyType,
} from "./classification";
import "./index.scss";

interface FilterOption<T extends string> {
  label: string;
  value: T | "";
}

function ShowcaseFilterGroup<T extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: Array<FilterOption<T>>;
  value: T | "";
  onChange: (value: T | "") => void;
}) {
  return (
    <div className="showcase-filter-group">
      <span className="showcase-filter-label">{label}</span>
      <div className="showcase-filter-options" role="group" aria-label={label}>
        {options.map((option) => (
          <button
            className={option.value === value ? "is-active" : ""}
            key={option.value || "all"}
            type="button"
            aria-pressed={option.value === value}
            onClick={() => onChange(option.value)}
          >
            {option.label}
          </button>
        ))}
      </div>
    </div>
  );
}

export default function GalleryPage() {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage || i18n.language;
  const [items, setItems] = useState<ShowcaseCase[]>([]);
  const [categories, setCategories] = useState<string[]>([]);
  const [keyword, setKeyword] = useState("");
  const [category, setCategory] = useState("");
  const [entryType, setEntryType] = useState<ShowcaseEntryType | "">("");
  const [technologyType, setTechnologyType] = useState<ShowcaseTechnologyType | "">("");
  const [isLoading, setIsLoading] = useState(true);
  const [hasError, setHasError] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    listShowcaseCases({}, { signal: controller.signal })
      .then((response) => {
        setItems((response.cases || []).filter((item) => item.gallery));
        const availableCategories = response.categories.filter(
          (item) => item !== "全部" && item !== "All",
        );
        setCategories(availableCategories);
        setCategory((current) =>
          availableCategories.includes(current) ? current : "",
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
  }, [locale]);

  const categoryOptions = useMemo<Array<FilterOption<string>>>(() => [
    { label: t("showcase.filters.all"), value: "" },
    ...categories.map((item) => ({ label: item, value: item })),
  ], [categories, t]);
  const entryTypeOptions = useMemo<Array<FilterOption<ShowcaseEntryType>>>(() => [
    { label: t("showcase.filters.all"), value: "" },
    { label: t("showcase.filters.capability.chat"), value: "chat" },
    { label: t("showcase.filters.capability.work"), value: "work" },
  ], [t]);
  const technologyTypeOptions = useMemo<Array<FilterOption<ShowcaseTechnologyType>>>(() => [
    { label: t("showcase.filters.all"), value: "" },
    { label: t("showcase.filters.technology.skill"), value: "skill" },
    { label: t("showcase.filters.technology.workflow"), value: "workflow" },
  ], [t]);

  const filteredItems = useMemo(() => {
    const normalizedKeyword = keyword.trim().toLowerCase();
    return items.filter((item) => {
      const matchesCategory = category === "" || item.category === category;
      const matchesEntryType = entryType === "" || showcaseEntryType(item.type) === entryType;
      const matchesTechnologyType = technologyType === ""
        || showcaseTechnologyType(item.type) === technologyType;
      const searchable = [
        item.title,
        item.description,
        item.category,
        item.prompt_short,
      ]
        .join(" ")
        .toLowerCase();
      return matchesCategory
        && matchesEntryType
        && matchesTechnologyType
        && (!normalizedKeyword || searchable.includes(normalizedKeyword));
    });
  }, [category, entryType, items, keyword, technologyType]);

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
        <div className="showcase-filter-groups">
          <ShowcaseFilterGroup
            label={t("showcase.filters.taskType")}
            options={categoryOptions}
            value={category}
            onChange={setCategory}
          />
          <ShowcaseFilterGroup
            label={t("showcase.filters.capabilityType")}
            options={entryTypeOptions}
            value={entryType}
            onChange={setEntryType}
          />
          <ShowcaseFilterGroup
            label={t("showcase.filters.technologyType")}
            options={technologyTypeOptions}
            value={technologyType}
            onChange={setTechnologyType}
          />
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
