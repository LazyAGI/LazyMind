import { CloseOutlined, FileTextOutlined } from "@ant-design/icons";

import "./index.scss";
import { Tooltip, message } from "antd";
import { readPreviewFile, urlPreviewTarget } from "../ArtifactPreview/target";
import { resolveMarkdownImageUrlAsync } from "@/modules/knowledge/utils/imageUrl";
import { downloadStream } from "../../utils/download";
import { useTranslation } from "react-i18next";

export interface ChatFile {
  name: string;
  uid: string;
  unavailable?: boolean;
  uri?: string;
}

interface Props {
  files: ChatFile[];
  onRemove?: (uid: string) => void;
}

const ChatFiles = (props: Props) => {
  const { files, onRemove } = props;
  const { t } = useTranslation();

  const download = async (item: ChatFile) => {
    if (!item.uri || item.unavailable) return;
    try {
      const url = await resolveMarkdownImageUrlAsync(item.uri);
      const bytes = await readPreviewFile(urlPreviewTarget(url, item.name), AbortSignal.timeout(60_000));
      downloadStream(new Blob([bytes]), item.name);
    } catch {
      message.error(t("chat.fileDownloadFailed"));
    }
  };

  return (
    <div className="chat-file-list">
      {files.map((item, index) => {
        return (
          <div className="chat-files-item" key={`img-${index}`}>
            <div className="chat-files-name">
              <FileTextOutlined />
              <Tooltip title={item.name}>
                {item.uri && !item.unavailable ? (
                  <button type="button" className="chat-files-name-title chat-files-download" onClick={() => void download(item)}>
                    {item.name}
                  </button>
                ) : (
                  <span className="chat-files-name-title">{item.name}{item.unavailable ? ` · ${t("chat.fork.attachmentUnavailable")}` : ""}</span>
                )}
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
