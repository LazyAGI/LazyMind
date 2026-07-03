import { ImgHTMLAttributes, useRef } from "react";
import { resolveMarkdownImageUrlAsync } from "@/modules/knowledge/utils/imageUrl";

const DEFAULT_ERROR_IMAGE =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";

interface IProps extends ImgHTMLAttributes<HTMLImageElement> {
  showErrorImage?: boolean;
  errorUrl?: string;
}

const CustomImage = (props: IProps) => {
  const { showErrorImage, errorUrl = DEFAULT_ERROR_IMAGE, ...rest } = props;
  const retriedRef = useRef(false);
  return (
    <img
      {...rest}
      alt={props.alt || " "}
      style={{ ...props.style }}
      onError={async (e) => {
        const target = e.target as HTMLImageElement;
        if (!retriedRef.current) {
          retriedRef.current = true;
          try {
            const refreshed = await resolveMarkdownImageUrlAsync(
              target.getAttribute("src") || props.src || "",
            );
            if (refreshed && refreshed !== target.src) {
              target.src = refreshed;
              return;
            }
          } catch {
            // Ignore refresh failure and fall back to error handling below.
          }
        }
        if (showErrorImage) {
          target.src = errorUrl;
        } else {
          target.style.display = "none";
        }
      }}
      onLoad={(e) => {
        retriedRef.current = false;
        const target = e.target as HTMLImageElement;
        target.style.display = "";
      }}
    />
  );
};

export default CustomImage;
