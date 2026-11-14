import { Modal } from "antd";
import { useEffect, useState } from "react";
import type { PaperGuideEvidence } from "@/api/generated/core-client";
export type GuidePreview = (
  e: PaperGuideEvidence,
) => Promise<{ url: string; width: number; height: number } | undefined>;
export default function PaperGuideAsset({
  evidence,
  preview,
  language,
}: {
  evidence: PaperGuideEvidence;
  preview: GuidePreview;
  language: string;
}) {
  const [image, setImage] = useState<{
    url: string;
    width: number;
    height: number;
  }>();
  const [failed, setFailed] = useState(false);
  const [large, setLarge] = useState(false);
  useEffect(() => {
    let stale = false;
    setImage(undefined);
    setFailed(false);
    void preview(evidence)
      .then((v) => {
        if (!stale) {
          setImage(v);
          setFailed(!v);
        }
      })
      .catch(() => {
        if (!stale) setFailed(true);
      });
    return () => {
      stale = true;
    };
  }, [evidence, preview]);
  const zh = language === "zh";
  return (
    <figure className="paper-guide-asset">
      <div className="paper-guide-image-frame">
        {image ? (
          <button
            className="paper-guide-image-button"
            onClick={() => setLarge(true)}
            aria-label={zh ? "放大论文原图" : "Enlarge original figure"}
          >
            <img
              src={image.url}
              width={image.width}
              height={image.height}
              alt={
                evidence.text ||
                `${zh ? "论文原图，PDF 第" : "Original figure, PDF page"} ${evidence.locator.page + 1}`
              }
            />
          </button>
        ) : (
          <div role="status">
            {failed
              ? zh
                ? "原图预览不可用，可打开原文查看。"
                : "Preview unavailable. Open the PDF to inspect the original."
              : zh
                ? "正在读取原图…"
                : "Loading original figure…"}
          </div>
        )}
      </div>
      <figcaption>
        {zh ? "原文" : "Source"} · PDF {evidence.locator.page + 1}
      </figcaption>
      {large && image && (
        <Modal
          open
          title={zh ? "论文原图" : "Original figure"}
          footer={null}
          onCancel={() => setLarge(false)}
          width="90vw"
        >
          <img
            style={{ maxWidth: "100%" }}
            src={image.url}
            alt={evidence.text}
          />
        </Modal>
      )}
    </figure>
  );
}
