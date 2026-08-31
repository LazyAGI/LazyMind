import { Image } from "antd";
import { useEffect, useState } from "react";
import { CloseCircleFilled } from "@ant-design/icons";
import { resolveMarkdownImageUrlAsync } from "@/modules/knowledge/utils/imageUrl";

import "./index.scss";

export interface ChatImage {
  base64: string;
  uid: string;
}

interface Props {
  images: ChatImage[];
  onRemove?: (uid: string) => void;
}

const ChatImages = (props: Props) => {
  const { images, onRemove } = props;

  return (
    <div className="chat-images-list">
      {images.map((item, index) => {
        return (
          <div className="chat-images-item" key={`img-${index}`}>
            <ResolvedChatImage src={item.base64} />
            {onRemove && (
              <div
                className="chat-images-remove"
                onClick={() => onRemove(item.uid)}
              >
                <CloseCircleFilled />
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
};

function ResolvedChatImage({ src }: { src: string }) {
  const [resolvedSrc, setResolvedSrc] = useState(src);
  useEffect(() => {
    let active = true;
    void resolveMarkdownImageUrlAsync(src).then((value) => {
      if (active) setResolvedSrc(value);
    });
    return () => { active = false; };
  }, [src]);
  return <Image src={resolvedSrc} height={52} />;
}

export default ChatImages;
