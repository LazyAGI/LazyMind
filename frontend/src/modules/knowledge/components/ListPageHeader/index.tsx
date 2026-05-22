import { FC, ReactElement, ReactNode } from "react";
import { Input, Select, Form, Button, Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import "./index.scss";

const { Search } = Input;

interface OptionItem {
  value: string;
  label: string;
}
interface Props {
  /** search params */
  placeholder?: string;
  allowClear?: boolean;

  /** extra */
  extra?: ReactElement | string;

  prefix?: ReactElement | string;

  
  sortOption?: OptionItem[];
  sortDefaultValue?: string;

  
  searchKey: string;
  sortKey?: string;

  
  btnText?: string;
  btnDisabled?: boolean;
  btnDisabledTooltip?: ReactNode;
  onClick?: () => void;
  onSearch: () => void;
}

const ListPageHeaderComponent: FC<Props> = ({
  placeholder = "",
  extra,
  sortOption,
  sortDefaultValue,
  searchKey = "keyword",
  sortKey = "sort",
  btnText = "",
  btnDisabled = false,
  btnDisabledTooltip,
  allowClear = true,
  onClick,
  onSearch,
  prefix,
}) => {
  const { t } = useTranslation();
  const defaultSortValue = sortDefaultValue
    ? sortDefaultValue
    : sortOption && sortOption?.length > 0
      ? sortOption[0].value
      : "";
  return (
    <div className="filter-container">
      {prefix}
      <Form.Item name={searchKey} label={t("common.search")} style={{ marginBottom: 0 }}>
        <Search
          placeholder={placeholder || t("common.pleaseInput")}
          allowClear={allowClear}
          className="search-input ghost-custom-border"
          variant="borderless"
          onSearch={onSearch}
        />
      </Form.Item>
      {extra}
      <div className="right-box">
        {sortOption && sortOption.length > 0 && (
          <div className="sort-box">
            <span>{t("common.sortBy")}</span>
            <Form.Item name={sortKey} noStyle initialValue={defaultSortValue}>
              <Select
                options={sortOption}
                variant={"underlined"}
                className="sort-select"
                onSearch={onSearch}
              />
            </Form.Item>
            <span>{t("common.sort")}</span>
          </div>
        )}
        {btnText && onClick && (
          <Tooltip title={btnDisabled ? btnDisabledTooltip : undefined}>
            <Button type="primary" disabled={btnDisabled} onClick={onClick}>
              {btnText || t("common.create")}
            </Button>
          </Tooltip>
        )}
      </div>
    </div>
  );
};

export default ListPageHeaderComponent;
