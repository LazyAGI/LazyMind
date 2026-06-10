import { AutoComplete } from "antd";
import { questionTypeOptions } from "../shared";

interface QuestionTypeSelectProps {
  value?: string;
  onChange?: (value: string) => void;
  onBlur?: () => void;
  placeholder?: string;
  allowClear?: boolean;
  options?: string[];
}

export default function QuestionTypeSelect({
  value,
  onChange,
  onBlur,
  placeholder = "请选择问题类型",
  allowClear,
  options,
}: QuestionTypeSelectProps) {
  const resolvedOptions = options ? options : questionTypeOptions;

  return (
    <AutoComplete
      allowClear={allowClear}
      value={value}
      onChange={(nextValue) => onChange?.(nextValue)}
      onBlur={onBlur}
      placeholder={placeholder}
      options={resolvedOptions.map((item) => ({ label: item, value: item }))}
      filterOption={(inputValue, option) =>
        `${option?.label || ""}`.toLowerCase().includes(inputValue.toLowerCase())
      }
    />
  );
}
