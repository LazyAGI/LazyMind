import { Select } from "antd";
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
  const resolvedOptions =
    options && options.length > 0 ? options : questionTypeOptions;

  return (
    <Select
      allowClear={allowClear}
      showSearch
      value={value}
      onChange={onChange}
      onBlur={onBlur}
      placeholder={placeholder}
      options={resolvedOptions.map((item) => ({ label: item, value: item }))}
      optionFilterProp="label"
    />
  );
}
