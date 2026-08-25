import { useState } from "react";
import type { ChatFileList } from "../ChatInput/types";
import { CloseCircleFilled } from "@ant-design/icons";
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
  const [previewUid, setPreviewUid] = useState<string | null>(null);

  const tempGroup = Object.groupBy(fileList, (item) => {
    const name = item.name ?? "";
    const suffix = name.substring(name.lastIndexOf(".")).toLowerCase();
    return allowedImageTypes.includes(suffix) ? "image" : "file";
  });

  const openNonImagePreview = (item: ChatFileList) => {
    const url = item.previewUrl;
    if (!url) {
      return;
    }
    window.open(url, "_blank", "noopener,noreferrer");
  };

  function renderImageItem(
    item: ChatFileList,
    index: number,
    isAllImage: boolean,
  ) {
    const suffix1 = item.suffix.substring(1).toUpperCase();
    const src = item.base64 || item.previewUrl || "";
    if (isAllImage) {
      return (
        <div className="chat-images-item" key={`img-${index}`}>
          <Image
            src={src}
            height={48}
            alt={item.name}
            preview={{
              visible: previewUid === item.uid,
              src,
              onVisibleChange: (visible) => {
                setPreviewUid(visible ? item.uid : null);
              },
            }}
            onClick={(event) => {
              event.stopPropagation();
              if (src) {
                setPreviewUid(item.uid);
              }
            }}
          />
          <CloseCircleFilled
            className="chat-files-remove"
            onClick={(event) => {
              event.stopPropagation();
              onRemove(item.uid);
            }}
          />
        </div>
      );
    }
    return (
      <div
        className="chat-files-item is-clickable"
        key={`img-${index}`}
        role="button"
        tabIndex={0}
        onClick={() => {
          if (src) {
            setPreviewUid(item.uid);
          }
        }}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            if (src) {
              setPreviewUid(item.uid);
            }
          }
        }}
      >
        <div className="chat-files-name">
          <div className="chatFileImage">
            <Image
              src={src}
              height={40}
              alt={item.name}
              preview={{
                visible: previewUid === item.uid,
                src,
                onVisibleChange: (visible) => {
                  setPreviewUid(visible ? item.uid : null);
                },
              }}
            />
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
          onClick={(event) => {
            event.stopPropagation();
            onRemove(item.uid);
          }}
        />
      </div>
    );
  }

  function renderFileItem(item: ChatFileList, index: number) {
    const suffix1 = item.suffix.substring(1).toUpperCase();
    return (
      <div
        className="chat-files-item is-clickable"
        key={`img-${index}`}
        role="button"
        tabIndex={0}
        onClick={() => openNonImagePreview(item)}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            openNonImagePreview(item);
          }
        }}
      >
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
          onClick={(event) => {
            event.stopPropagation();
            onRemove(item.uid);
          }}
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
