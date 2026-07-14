import { Popover, Tooltip } from "antd";
import { useEffect, useState } from "react";
import DatabaseIcon from "../../assets/icons/database.svg?react";
import { UserDatabaseSummary } from "@/api/generated/knowledge-client";
import { CheckOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import "./index.scss";

interface SelectDatabaseProps {
  currentDatabase?: string;
  onChangeFn?: (databaseId: string) => void;
}

function SelectDatabase(props: SelectDatabaseProps) {
  const { t } = useTranslation();
  const { currentDatabase = "", onChangeFn } = props;
  const [open, setOpen] = useState(false);
  const [databaseBaseList, setDatabaseBaseList] = useState<
    UserDatabaseSummary[]
  >([]);
  const [selectedId, setSelectedId] = useState<string>("");

  useEffect(() => {
    setSelectedId(currentDatabase);
  }, [currentDatabase]);

  function getDatabaseBaseList() {
    // DatabaseBaseServiceApi()
    //   .databaseServiceGetUserDatabaseSummaries({})
    //   .then((res) => {
    //     setDatabaseBaseList((res?.data as UserDatabaseSummary[]) || []);
    //   });
    setDatabaseBaseList([]);
  }

  useEffect(() => {
    getDatabaseBaseList();
  }, []);

  function renderDatabaseContent() {
    return (
      <div className="chatSelectDatabaseBox">
        {databaseBaseList?.map((item) => {
          const isSelected = selectedId.includes(item.id || "");
          return (
            <div
              key={item.id}
              className={`chat-selector-list-item ${isSelected ? "selected" : ""}`}
              onClick={() => {
                if (!isSelected) {
                  setSelectedId(item?.id ?? "");
                  onChangeFn?.(item?.id ?? "");
                } else {
                  setSelectedId("");
                  onChangeFn?.("");
                }
              }}
            >
              <Tooltip title={item.name} placement="right">
                <span className="chat-selector-item-label">{item.name}</span>
              </Tooltip>
              {isSelected && (
                <CheckOutlined className="chat-selector-check-icon" />
              )}
            </div>
          );
        })}
      </div>
    );
  }

  return (
    <Popover
      content={renderDatabaseContent()}
      trigger="click"
      open={open}
      onOpenChange={(visible) => setOpen(visible)}
    >
      <div
        className={`input-bottom-actions-left-item ${open || selectedId?.length ? "selected" : ""}`}
      >
        <DatabaseIcon />
        {t("chat.configDatabase")}
      </div>
    </Popover>
  );
}

export default SelectDatabase;
