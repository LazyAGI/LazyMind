import { Modal } from "antd";
import { useEffect, useRef, useState } from "react";
import type { PaperGuideEvidence } from "@/api/generated/core-client";
export type GuidePreview = (
  e: PaperGuideEvidence,
) => Promise<{ url: string; width: number; height: number } | undefined>;
export default function PaperGuideAsset({
  evidence,
  preview,
  language,
  unavailable = false,
}: {
  evidence?: PaperGuideEvidence;
  unavailable?: boolean;
  preview: GuidePreview;
  language: string;
}) {
  const [image, setImage] = useState<{
    evidence: PaperGuideEvidence;
    url: string;
    width: number;
    height: number;
  }>();
  const [failed, setFailed] = useState(false);
  const [large, setLarge] = useState(false);
  const opener = useRef<HTMLButtonElement>();
  useEffect(() => {
    let stale = false;
    setImage(undefined);
    setFailed(false);
    setLarge(false);
    opener.current = undefined;
    if (!evidence || unavailable) return;
    void preview(evidence)
      .then((v) => {
        if (!stale) {
          setImage(v ? { ...v, evidence } : undefined);
          setFailed(!v);
        }
      })
      .catch(() => {
        if (!stale) setFailed(true);
      });
    return () => {
      stale = true;
    };
  }, [evidence, preview, unavailable]);
  const zh = language === "zh";
  return (
    <figure className="paper-guide-asset">
      <div className="paper-guide-image-frame">
        {image && image.evidence === evidence && evidence && !unavailable ? (
          <button
            className="paper-guide-image-button"
            onClick={(event) => {
              opener.current = event.currentTarget;
              setLarge(true);
            }}
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
            {failed || unavailable
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
        {zh ? "原文" : "Source"} · PDF{" "}
        {evidence ? evidence.locator.page + 1 : "—"}
      </figcaption>
      {image && evidence && !unavailable && (
        <Modal
          open={large}
          focusTriggerAfterClose={false}
          afterClose={() => {
            const button = opener.current;
            if (button?.isConnected && !button.closest("[hidden]"))
              button.focus({ preventScroll: true });
          }}
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
