import { CloseOutlined, FileTextOutlined } from "@ant-design/icons";

import "./index.scss";
import { Tooltip } from "antd";
import { useTranslation } from "react-i18next";

export interface ChatFile {
  name: string;
  uid: string;
  unavailable?: boolean;
}

interface Props {
  files: ChatFile[];
  onRemove?: (uid: string) => void;
}

const ChatFiles = (props: Props) => {
  const { files, onRemove } = props;
  const { t } = useTranslation();

  return (
    <div className="chat-file-list">
      {files.map((item, index) => {
        return (
          <div className="chat-files-item" key={`img-${index}`}>
            <div className="chat-files-name">
              <FileTextOutlined />
              <Tooltip title={item.name}>
                <span className="chat-files-name-title">{item.name}{item.unavailable ? ` · ${t("chat.fork.attachmentUnavailable")}` : ""}</span>
              </Tooltip>
            </div>
            {onRemove && (
              <div
                className="chat-files-remove"
                onClick={() => onRemove(item.uid)}
              >
                <CloseOutlined />
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
};

export default ChatFiles;
