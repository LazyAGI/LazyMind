import { Modal } from "antd";
import PaperGuideText from "./PaperGuideText";
import { useEffect, useRef, useState } from "react";
import type { PaperGuideEvidence, PaperGuideOriginalDisplay } from "@/api/generated/core-client";
type AssetImage = { url: string; width: number; height: number; mode: "region" | "page" };
export type GuidePreview = (
  e: PaperGuideEvidence,
  original?: PaperGuideOriginalDisplay,
) => Promise<AssetImage | undefined>;
export default function PaperGuideAsset({
  evidence,
  originalDisplay,
  preview,
  language,
  unavailable = false,
  onEvidence,
  onOpenPage,
  compactCaption = false,
  captionText,
}: {
  compactCaption?: boolean;
  captionText?: string;
  evidence?: PaperGuideEvidence;
  originalDisplay?: PaperGuideOriginalDisplay | null;
  unavailable?: boolean;
  onOpenPage?: (evidence: PaperGuideEvidence) => void;
  onEvidence?: (id: string, opener: HTMLButtonElement) => void;
  preview: GuidePreview;
  language: string;
}) {
  const [image, setImage] = useState<AssetImage & {
    evidence: PaperGuideEvidence;
    preview: GuidePreview;
    originalDisplay?: PaperGuideOriginalDisplay | null;
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
    void preview(evidence, originalDisplay ?? undefined)
      .then((v) => {
        if (!stale) {
          setImage(v ? { ...v, evidence, originalDisplay, preview } : undefined);
          setFailed(!v);
        }
      })
      .catch(() => {
        if (!stale) setFailed(true);
      });
    return () => {
      stale = true;
    };
  }, [evidence, originalDisplay, preview, unavailable]);
  const zh = language === "zh";
  const loaded = !unavailable && image?.evidence === evidence &&
    image?.preview === preview && image?.originalDisplay === originalDisplay ? image : undefined;
  const table = evidence?.source_type === "table";
  const formula = evidence?.source_type === "formula";
  const page = evidence ? evidence.locator.page + 1 : undefined;
  const regionLabel = formula ? (zh ? "原公式" : "Original formula") : (zh ? "原图区域" : "Original figure region");
  const pageLabel = loaded?.mode === "page" ? (zh ? "原文页" : "Source page") : regionLabel;
  const label = table ? (zh ? "表格起始页" : "Table start page") : loaded ? pageLabel : (zh ? "原文" : "Source");
  const compactPage = compactCaption && !table && !formula && loaded?.mode === "page";
  const pageText = zh ? `PDF 第 ${page ?? "—"} 页` : `PDF page ${page ?? "—"}`;
  const alt = captionText || originalDisplay?.caption || (table ? evidence?.text : "") || `${label} · ${pageText}`;
  const enlargeLabel = table || (!formula && loaded?.mode !== "page")
    ? (zh ? "放大论文原图" : "Enlarge original figure")
    : loaded?.mode === "page"
      ? (zh ? "放大原文页" : "Enlarge source page")
      : (zh ? "放大原公式" : "Enlarge original formula");
  return (
    <figure className={compactPage ? "paper-guide-asset paper-guide-page-link" : "paper-guide-asset"}>
      <div className="paper-guide-image-frame">
        {loaded ? (
          <button
            className={compactPage ? "paper-guide-citation" : "paper-guide-image-button"}
            onClick={(event) => {
              opener.current = event.currentTarget;
              setLarge(true);
            }}
            aria-label={compactPage ? undefined : enlargeLabel}
          >
            {compactPage ? (zh ? `查看原文第 ${page} 页` : `View PDF page ${page}`) : <img
              src={loaded.url}
              width={loaded.width}
              height={loaded.height}
              alt={alt}
              onError={() => { setImage(undefined); setFailed(true); }}
            />}
          </button>
        ) : (
          <div role="status">
            {failed || unavailable
              ? zh
                ? "原件预览不可用，可打开原文查看。"
                : "Preview unavailable. Open the PDF to inspect the original."
              : zh
                ? "正在读取原件…"
                : "Loading original…"}
          </div>
        )}
      </div>
      {compactCaption && !table && !formula ? <figcaption>
        <PaperGuideText mapSource={false} text={(() => {
          const source = originalDisplay?.caption?.trim() || "";
          const prefix = /^(?:论文图|Figure|Fi\s+ure|Fig\.?|图)\s*([A-Za-z]?\d+(?:\.\d+)*[A-Za-z]?)\s*[:：.．]?\s*/i;
          const number = originalDisplay?.number || source.match(prefix)?.[1] || evidence?.text?.match(prefix)?.[1];
          const title = (captionText?.trim() || source).replace(prefix, "");
          const label = zh ? `论文图${number || ""}` : `Figure${number ? ` ${number}` : ""}`;
          return title ? label + (zh ? "：" : ": ") + title : label;
        })()} />
      </figcaption> : <>
      <figcaption>
        {label} · {pageText}
        {loaded?.mode === "page" && !table && <p>{formula ? (zh ? "未定位到单独公式区域。" : "No separate formula region is available.") : (zh ? "未定位到单独原图区域。" : "No separate figure region is available.")}</p>}
        {originalDisplay?.caption && <p>{originalDisplay.caption}</p>}
        {!unavailable && evidence && onOpenPage && <button type="button" className="paper-guide-citation" onClick={() => onOpenPage({ ...evidence, locator: { kind: "page", page: evidence.locator.page } })}>{zh ? `打开 PDF 第 ${page} 页` : `Open PDF page ${page}`}</button>}
      </figcaption>
      {!unavailable && onEvidence && !!evidence?.fragment_evidence_ids?.length && <nav aria-label={zh ? "表格逐页来源" : "Physical table sources"}><div>{evidence.fragment_evidence_ids.map((id, index) => <button key={id} onClick={event => onEvidence(id, event.currentTarget)}>{zh ? `表格来源片段 ${index + 1}` : `Table source fragment ${index + 1}`}</button>)}</div></nav>}
      </>}
      {loaded && (
        <Modal
          open={large}
          focusTriggerAfterClose={false}
          afterClose={() => {
            const button = opener.current;
            if (button?.isConnected && !button.closest("[hidden]"))
              button.focus({ preventScroll: true });
          }}
          title={label}
          footer={null}
          onCancel={() => setLarge(false)}
          centered
          width="fit-content"
        >
          <img
            width={loaded.width}
            height={loaded.height}
            style={{ display: "block", margin: "0 auto", width: "auto", height: "auto", maxWidth: "100%", maxHeight: "75dvh" }}
            src={loaded.url}
            alt={loaded.mode === "page" ? pageText : alt}
          />
        </Modal>
      )}
    </figure>
  );
}
