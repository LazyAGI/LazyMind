import { Select } from "antd";
import { questionTypeOptions } from "../shared";

interface QuestionTypeSelectProps {
  value?: string;
  onChange?: (value: string) => void;
  placeholder?: string;
  allowClear?: boolean;
}

export default function QuestionTypeSelect({
  value,
  onChange,
  placeholder = "请选择问题类型",
  allowClear,
}: QuestionTypeSelectProps) {
  return (
    <Select
      allowClear={allowClear}
      showSearch
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      options={questionTypeOptions.map((item) => ({ label: item, value: item }))}
      optionFilterProp="label"
    />
  );
}

