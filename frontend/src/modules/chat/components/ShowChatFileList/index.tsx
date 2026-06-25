import React from "react";
import { ChatFileList } from "../ChatInput";
import {
  CloseCircleFilled,
  CloseOutlined,
  FileTextOutlined,
} from "@ant-design/icons";
import { Image, Tooltip } from "antd";
import { allowedImageTypes } from "../ImageUpload";
import "./index.scss";
import FileIcon from "../../assets/icons/file.svg?react";

interface ShowChatFileListProps {
  fileList: ChatFileList[];
  onRemove: (uid: string) => void;
}

function ShowChatFileList(props: ShowChatFileListProps) {
  const { fileList, onRemove } = props;

  const tempGroup = Object.groupBy(fileList, (item) => {
    const name = item.name ?? '';
    const suffix = name.substring(name.lastIndexOf(".")).toLowerCase();
    return allowedImageTypes.includes(suffix) ? "image" : "file";
  });

  function renderImageItem(
    item: ChatFileList,
    index: number,
    isAllImage: boolean,
  ) {
    const suffix1 = item.suffix.substring(1).toUpperCase();
    if (isAllImage) {
      return (
        <div className={"chat-images-item"} key={`img-${index}`}>
          <Image src={item.base64} height={48} />
          <CloseCircleFilled
            className="chat-files-remove"
            onClick={() => onRemove(item.uid)}
          />
        </div>
      );
    }
    return (
      <div className="chat-files-item" key={`img-${index}`}>
        <div className="chat-files-name">
          <div className="chatFileImage">
            <Image src={item.base64} height={40} />
          </div>
          <div className="chat-file-box">
            <Tooltip title={item.name}>
              <span className="chat-files-name-title">{item.name}</span>
            </Tooltip>
            <div className="chat-files-file-info">
              <span>{suffix1}</span>
              <span style={{ marginLeft: 8 }}>{item.size}</span>
            </div>
          </div>
        </div>
        <CloseCircleFilled
          className="chat-files-remove"
          onClick={() => onRemove(item.uid)}
        />
      </div>
    );
  }
  function renderFileItem(item: ChatFileList, index: number) {
    const suffix1 = item.suffix.substring(1).toUpperCase();
    return (
      <div className="chat-files-item" key={`img-${index}`}>
        <div className="chat-files-name">
          <FileIcon />
          <div className="chat-file-box">
            <Tooltip title={item.name}>
              <span className="chat-files-name-title">{item.name}</span>
            </Tooltip>
            <div className="chat-files-file-info">
              <span>{suffix1}</span>
              <span style={{ marginLeft: 8 }}>{item.size}</span>
            </div>
          </div>
        </div>
        <CloseCircleFilled
          className="chat-files-remove"
          onClick={() => onRemove(item.uid)}
        />
      </div>
    );
  }

  function renderContentFn() {
    if (!tempGroup?.file?.length) {
      return fileList?.map((it, i) => renderImageItem(it, i, true));
    }
    return fileList?.map((it, i) => {
      const suffix = it.name?.substring(it.name.lastIndexOf(".")).toLowerCase();
      if (allowedImageTypes.includes(suffix ?? "")) {
        return renderImageItem(it, i, false);
      }
      return renderFileItem(it, i);
    });
  }

  return (
    <div className="ShowFileListBox">
      <div className="ShowFileListContainer">{renderContentFn()}</div>
    </div>
  );
}

export default ShowChatFileList;
