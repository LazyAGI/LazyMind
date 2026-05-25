import { ALL_TAGS } from "@/modules/knowledge/constants/common";
import { message, Select } from "antd";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

const EMPTY_TAGS: string[] = [];

interface TagSelectProps {
  tags: string[];
  maxTagLength?: number;
  maxTagLengthMessage?: string;
  showOverLengthInputError?: boolean;
  onLengthErrorChange?: (hasError: boolean) => void;
  value?: string[];
  onChange?: (value: string[]) => void;
}

const TagSelect = ({
  tags,
  maxTagLength = 100,
  maxTagLengthMessage,
  showOverLengthInputError = false,
  onLengthErrorChange,
  value,
  onChange,
}: TagSelectProps) => {
  const { t } = useTranslation();
  const MAX_TAG_COUNT = 10;
  const selectedTags = value ?? EMPTY_TAGS;

  const [searchValue, setSearchValue] = useState("");
  const hasSelectedOverLengthTag = useMemo(
    () => selectedTags.some((tag) => Array.from(tag).length > maxTagLength),
    [maxTagLength, selectedTags],
  );

  const notifyLengthError = useCallback((currentInput: string, currentTags = selectedTags) => {
    onLengthErrorChange?.(
      (showOverLengthInputError &&
        Array.from(currentInput).length > maxTagLength) ||
        currentTags.some((tag) => Array.from(tag).length > maxTagLength),
    );
  }, [maxTagLength, onLengthErrorChange, selectedTags, showOverLengthInputError]);

  useEffect(() => {
    notifyLengthError(searchValue);
  }, [
    hasSelectedOverLengthTag,
    maxTagLength,
    notifyLengthError,
    selectedTags,
    searchValue,
    showOverLengthInputError,
  ]);

  function handleSearch(val: string) {
    if (!showOverLengthInputError && Array.from(val).length > maxTagLength) {
      setSearchValue("");
      notifyLengthError("");
      return;
    }
    setSearchValue(val);
    notifyLengthError(val);
  }

  function handleChange(nextValue: string[]) {
    const normalizedTags = Array.from(
      new Set((nextValue || []).map((tag) => tag.trim()).filter(Boolean)),
    );
    const validLengthTags = normalizedTags.filter(
      (tag) => Array.from(tag).length <= maxTagLength,
    );

    if (validLengthTags.length < normalizedTags.length) {
      message.warning(
        maxTagLengthMessage ||
          t("knowledge.singleTagMaxLength", { count: maxTagLength }),
      );
      setSearchValue("");
    }

    if (validLengthTags.length > MAX_TAG_COUNT) {
      message.warning(t("knowledge.maxTenTags"));
      setSearchValue("");
      notifyLengthError("", validLengthTags.slice(0, MAX_TAG_COUNT));
      onChange?.(validLengthTags.slice(0, MAX_TAG_COUNT));
      return;
    }

    notifyLengthError("", validLengthTags);
    onChange?.(validLengthTags);
  }

  return (
    <Select
      mode="tags"
      tokenSeparators={[","]}
      searchValue={searchValue}
      value={selectedTags}
      onChange={handleChange}
      onSearch={handleSearch}
      options={tags
        .filter((tag) => tag !== ALL_TAGS)
        .map((tag) => {
          return { value: tag, name: tag };
        })}
      onInputKeyDown={(e) => {
        if (
          !showOverLengthInputError &&
          Array.from(searchValue).length >= maxTagLength &&
          e.key !== "Backspace"
        ) {
          e.preventDefault();
        }
      }}
      status={
        (showOverLengthInputError &&
          Array.from(searchValue).length > maxTagLength) ||
        hasSelectedOverLengthTag
          ? "error"
          : undefined
      }
      onSelect={() => {
        setSearchValue("");
        notifyLengthError("");
      }}
      placeholder={t("knowledge.selectTagPlaceholder")}
    />
  );
};

export default TagSelect;
