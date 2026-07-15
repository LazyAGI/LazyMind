import { AutoComplete } from "antd";
import { useTranslation } from "react-i18next";
import { questionTypeI18nKeys, questionTypeOptions } from "../shared";

interface QuestionTypeSelectProps {
  value?: string;
  onChange?: (value: string) => void;
  onBlur?: () => void;
  placeholder?: string;
  allowClear?: boolean;
  options?: string[];
  showAllOptions?: boolean;
}

export default function QuestionTypeSelect({
  value,
  onChange,
  onBlur,
  placeholder,
  allowClear,
  options,
  showAllOptions = false,
}: QuestionTypeSelectProps) {
  const { t } = useTranslation();
  const resolvedOptions = options ? options : questionTypeOptions;

  return (
    <AutoComplete
      allowClear={allowClear}
      value={value}
      onChange={(nextValue) => onChange?.(nextValue)}
      onBlur={onBlur}
      placeholder={placeholder || t("datasetManagement.detail.placeholders.questionType")}
      options={resolvedOptions.map((item) => ({
        label: t(questionTypeI18nKeys[item] || item),
        value: item,
      }))}
      filterOption={
        showAllOptions
          ? false
          : (inputValue, option) =>
              `${option?.label || ""}`
                .toLowerCase()
                .includes(inputValue.toLowerCase())
      }
    />
  );
}
