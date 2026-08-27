import { useEffect, useRef } from "react";
import { Button, Popover } from "antd";
import { DownOutlined } from "@ant-design/icons";
import type { TFunction } from "i18next";

import { ALL_TAGS } from "@/modules/knowledge/constants/common";
import type {
  KnowledgeMineCloudSource,
  KnowledgeMineSort,
} from "./knowledgeMineFilters";

interface KnowledgeMineFilterPopoverProps {
  t: TFunction;
  tags: string[];
  primaryFilter: "tags" | "cloudSource";
  open: boolean;
  selectedTag: string;
  selectedSort: KnowledgeMineSort;
  selectedCloudSource: KnowledgeMineCloudSource;
  onOpenChange: (open: boolean) => void;
  onTagChange: (tag: string) => void;
  onSortChange: (sort: KnowledgeMineSort) => void;
  onCloudSourceChange: (source: KnowledgeMineCloudSource) => void;
}

interface FilterOption<T extends string> {
  label: string;
  value: T;
}

const sortValues: KnowledgeMineSort[] = [
  "all",
  "recent_used",
  "most_used",
  "latest_updated",
];

const cloudSourceValues: Exclude<KnowledgeMineCloudSource, "all">[] = [
  "local",
  "feishu",
  "notion",
];

export default function KnowledgeMineFilterPopover({
  t,
  tags,
  primaryFilter,
  open,
  selectedTag,
  selectedSort,
  selectedCloudSource,
  onOpenChange,
  onTagChange,
  onSortChange,
  onCloudSourceChange,
}: KnowledgeMineFilterPopoverProps) {
  const triggerRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;

    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      onOpenChange(false);
      window.requestAnimationFrame(() => triggerRef.current?.focus());
    };
    document.addEventListener("keydown", closeOnEscape);
    return () => document.removeEventListener("keydown", closeOnEscape);
  }, [onOpenChange, open]);

  const activeCount =
    Number(selectedSort !== "all") +
    Number(
      primaryFilter === "tags"
        ? selectedTag !== ALL_TAGS
        : selectedCloudSource !== "all",
    );

  const tagOptions: FilterOption<string>[] = [
    { value: ALL_TAGS, label: t("knowledge.mineAllTags") },
    ...tags.map((tag) => ({ value: tag, label: tag })),
  ];
  const sortOptions: FilterOption<KnowledgeMineSort>[] = sortValues.map(
    (value) => ({
      value,
      label: t(`knowledge.mineSort.${value}`),
    }),
  );
  const cloudSourceOptions: FilterOption<KnowledgeMineCloudSource>[] =
    cloudSourceValues.map((value) => ({
      value,
      label: t(`knowledge.mineCloudSource.${value}`),
    }));

  const renderOptions = <T extends string>(
    options: FilterOption<T>[],
    selected: string,
    onSelect: (value: T) => void,
  ) => (
    <div className="knowledge-mine-filter-options">
      {options.map((option) => {
        const isSelected = selected === option.value;
        return (
          <button
            key={option.value}
            type="button"
            role="option"
            aria-selected={isSelected}
            className={`knowledge-mine-filter-option ${isSelected ? "is-selected" : ""}`}
            onClick={(event) => {
              event.stopPropagation();
              onSelect(option.value);
            }}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );

  const content = (
    <div
      className="knowledge-mine-filter-panel"
      role="dialog"
      aria-label={t("knowledge.filterAndSortAria")}
    >
      {primaryFilter === "tags" ? (
        <div
          className="knowledge-mine-filter-section"
          role="group"
          aria-label={t("knowledge.tags")}
        >
          <div className="knowledge-mine-filter-section-title">
            {t("knowledge.tags")}
          </div>
          {renderOptions(tagOptions, selectedTag, onTagChange)}
        </div>
      ) : (
        <div
          className="knowledge-mine-filter-section"
          role="group"
          aria-label={t("knowledge.cloudSourceLabel")}
        >
          <div className="knowledge-mine-filter-section-title">
            {t("knowledge.cloudSourceLabel")}
          </div>
          {renderOptions(
            cloudSourceOptions,
            selectedCloudSource,
            (value) => {
              onCloudSourceChange(
                selectedCloudSource === value ? "all" : value,
              );
            },
          )}
        </div>
      )}
      <div
        className="knowledge-mine-filter-section"
        role="group"
        aria-label={t("knowledge.sortLabel")}
      >
        <div className="knowledge-mine-filter-section-title">
          {t("knowledge.sortLabel")}
        </div>
        {renderOptions(sortOptions, selectedSort, onSortChange)}
      </div>
    </div>
  );

  return (
    <Popover
      arrow={false}
      content={content}
      open={open}
      placement="bottomRight"
      trigger="click"
      classNames={{ root: "knowledge-mine-filter-popover" }}
      onOpenChange={onOpenChange}
    >
      <Button
        ref={triggerRef}
        className={`knowledge-mine-filter-trigger ${activeCount > 0 ? "is-active" : ""}`}
        aria-expanded={open}
        aria-haspopup="dialog"
        title={
          activeCount > 0
            ? t("knowledge.filterAndSortActive", { count: activeCount })
            : t("knowledge.filterAndSortHint")
        }
      >
        <span>{t("knowledge.filterAndSort")}</span>
        <DownOutlined aria-hidden="true" className="knowledge-mine-filter-arrow" />
      </Button>
    </Popover>
  );
}
