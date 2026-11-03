import { useCallback, useEffect, useRef } from "react";
import { init } from "pptx-preview";
import i18n from "@/i18n";

interface RenderPptProps {
  fileData: ArrayBuffer;
  onPreviewError?: () => void;
}

const RenderPpt = (props: RenderPptProps) => {
  const { fileData } = props;
  const errorCallback = useRef(props.onPreviewError);
  errorCallback.current = props.onPreviewError;

  const showFile = useRef<HTMLDivElement>(null);

  const previewPptx = useCallback(async (arrayBuffer: ArrayBuffer) => {
    if (!showFile.current) {
      return;
    }

    try {
      const previewContainer = document.createElement("div");
      previewContainer.style.cssText = `
        width: 100%;
        height: 100%;
        overflow: auto;
        background: #f5f5f5;
      `;

      const pptxPreview = init(previewContainer, {
        width: 800,
        height: 600,
        mode: "slide",
      });

      await pptxPreview.preview(arrayBuffer);

      if (showFile.current) {
        showFile.current.innerHTML = "";
        showFile.current.appendChild(previewContainer);
      }
    } catch (err) {
      if (!showFile.current) return;
      if (errorCallback.current) { errorCallback.current(); return; }
      console.error("PPTX preview error:", err);

      showFile.current.innerHTML = "";

      const errorContainer = document.createElement("div");
      errorContainer.style.cssText = `
        width: 100%;
        height: 100%;
        overflow: auto;
        padding: 20px;
        background: #f5f5f5;
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;
      `;

      const infoDiv = document.createElement("div");
      infoDiv.style.cssText = `
        background: white;
        padding: 40px;
        border-radius: 8px;
        box-shadow: 0 2px 8px rgba(0,0,0,0.1);
        text-align: center;
        max-width: 500px;
      `;

      const title = document.createElement("h3");
      title.textContent = i18n.t("knowledge.pptPreviewFailed");
      title.style.cssText = `
        margin: 0 0 16px 0;
        color: #d32f2f;
        font-size: 20px;
        font-weight: bold;
      `;

      const description = document.createElement("p");
      description.textContent = i18n.t("knowledge.pptPreviewFailedWithError", {
        error: err instanceof Error ? err.message : i18n.t("knowledge.unknownError"),
      });
      description.style.cssText = `
        margin: 0 0 20px 0;
        color: #666;
        font-size: 14px;
        line-height: 1.5;
      `;

      const fileInfo = document.createElement("div");
      fileInfo.style.cssText = `
        background: #f8f9fa;
        padding: 12px;
        border-radius: 4px;
        font-family: monospace;
        font-size: 12px;
        color: #495057;
      `;
      fileInfo.textContent = i18n.t("knowledge.fileSizeLabel", {
        size: (arrayBuffer.byteLength / 1024).toFixed(1),
      });

      infoDiv.appendChild(title);
      infoDiv.appendChild(description);
      infoDiv.appendChild(fileInfo);

      errorContainer.appendChild(infoDiv);
      showFile.current.appendChild(errorContainer);
    }
  }, []);

  useEffect(() => {
    previewPptx(fileData);
  }, [fileData, previewPptx]);

  return (
    <div className="file-viewer-container">
      <div ref={showFile} className="file-viewer-content"></div>
    </div>
  );
};

export default RenderPpt;
