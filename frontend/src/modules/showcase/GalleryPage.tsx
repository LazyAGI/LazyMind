import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ArrowLeftOutlined, SearchOutlined } from "@ant-design/icons";
import { Select } from "antd";
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

function ShowcaseCategoryFilter({
  label,
  moreLabel,
  options,
  value,
  onChange,
}: {
  label: string;
  moreLabel: string;
  options: Array<FilterOption<string>>;
  value: string;
  onChange: (value: string) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const labelRef = useRef<HTMLSpanElement>(null);
  const measureRef = useRef<HTMLDivElement>(null);
  const [visibleCount, setVisibleCount] = useState(options.length);

  const updateVisibleCount = useCallback(() => {
    const container = containerRef.current;
    const labelElement = labelRef.current;
    const measureElement = measureRef.current;
    if (!container || !labelElement || !measureElement) return;
    if (container.clientWidth <= 0) {
      setVisibleCount(options.length);
      return;
    }

    const optionWidths = Array.from(measureElement.children).map(
      (element) => (element as HTMLElement).offsetWidth,
    );
    const optionsGap = 6;
    const groupGap = 8;
    const moreWidth = 100;
    const availableWidth = container.clientWidth - labelElement.offsetWidth - groupGap;
    const totalWidth = optionWidths.reduce((total, width) => total + width, 0)
      + Math.max(0, optionWidths.length - 1) * optionsGap;

    if (totalWidth <= availableWidth) {
      setVisibleCount(options.length);
      return;
    }

    const inlineWidth = Math.max(0, availableWidth - moreWidth - optionsGap);
    let usedWidth = 0;
    let nextVisibleCount = 0;
    for (const width of optionWidths) {
      const nextWidth = usedWidth + (nextVisibleCount > 0 ? optionsGap : 0) + width;
      if (nextWidth > inlineWidth) break;
      usedWidth = nextWidth;
      nextVisibleCount += 1;
    }
    setVisibleCount(Math.max(1, nextVisibleCount));
  }, [options]);

  useLayoutEffect(() => {
    updateVisibleCount();
    const container = containerRef.current;
    if (!container) return undefined;
    if (typeof ResizeObserver !== "undefined") {
      const observer = new ResizeObserver(updateVisibleCount);
      observer.observe(container);
      return () => observer.disconnect();
    }
    window.addEventListener("resize", updateVisibleCount);
    return () => window.removeEventListener("resize", updateVisibleCount);
  }, [updateVisibleCount]);

  const inlineOptions = options.slice(0, visibleCount);
  const overflowOptions = options.slice(visibleCount);

  return (
    <div className="showcase-filter-group" ref={containerRef}>
      <span className="showcase-filter-label" ref={labelRef}>{label}</span>
      <div className="showcase-filter-options" role="group" aria-label={label}>
        {inlineOptions.map((option) => (
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
      {overflowOptions.length > 0 && (
        <Select
          aria-label={moreLabel}
          className="showcase-more-filter"
          options={overflowOptions}
          placeholder={moreLabel}
          popupMatchSelectWidth
          value={overflowOptions.some((option) => option.value === value) ? value : undefined}
          onChange={onChange}
        />
      )}
      <div className="showcase-filter-measure" ref={measureRef} aria-hidden="true">
        {options.map((option) => (
          <button key={option.value || "all"} type="button" tabIndex={-1}>{option.label}</button>
        ))}
      </div>
    </div>
  );
}

function ShowcaseSelectFilter<T extends string>({
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
    <label className="showcase-select-filter">
      <span className="showcase-filter-label">{label}</span>
      <Select
        aria-label={label}
        className="showcase-filter-select"
        options={options}
        popupMatchSelectWidth
        value={value}
        onChange={onChange}
      />
    </label>
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
        const availableCategories = (response.categories ?? []).filter(
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
        ...(item.tasks ?? []).flatMap((task) => [
          task.title,
          task.description,
          task.prompt_short,
        ]),
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
          <ShowcaseCategoryFilter
            label={t("showcase.filters.taskType")}
            moreLabel={t("showcase.filters.more")}
            options={categoryOptions}
            value={category}
            onChange={setCategory}
          />
          <ShowcaseSelectFilter
            label={t("showcase.filters.capabilityType")}
            options={entryTypeOptions}
            value={entryType}
            onChange={setEntryType}
          />
          <ShowcaseSelectFilter
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
