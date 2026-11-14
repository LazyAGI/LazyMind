import { FormulaMathText } from "@/components/ui/FormulaMathText";
import type {
  PaperGuideEvidenceResponse,
  PaperGuideEvidence as Evidence,
} from "@/api/generated/core-client";
export default function PaperGuideEvidence({
  value,
  currentSourceHash,
  revisionSourceHash,
  language,
  onNavigate,
  onClose,
  children,
}: {
  value: PaperGuideEvidenceResponse;
  currentSourceHash?: string;
  revisionSourceHash: string;
  language: string;
  onNavigate: (e: Evidence) => void;
  onClose: () => void;
  children?: React.ReactNode;
}) {
  const zh = language === "zh",
    e = value.evidence;
  const current =
    value.source_current &&
    !!currentSourceHash &&
    currentSourceHash === revisionSourceHash;
  return (
    <div className="paper-guide-evidence">
      <div>
        <strong>{zh ? "论文原文来源" : "Original paper source"}</strong>
        <button aria-label={zh ? "关闭来源" : "Close source"} onClick={onClose}>
          ×
        </button>
      </div>
      <p>
        {e.source_type} ·{" "}
        {zh
          ? `PDF 第 ${e.locator.page + 1} 页`
          : `PDF page ${e.locator.page + 1}`}
      </p>
      <blockquote>{e.text}</blockquote>
      {e.locator.latex && (
        <div className="paper-guide-equation">
          <FormulaMathText text={e.locator.latex} mathOnly />
        </div>
      )}
      {children}
      {!current && (
        <p role="status">
          {zh
            ? "源文件已变化或尚未核对，不能用历史坐标定位当前 PDF。"
            : "Source changed or unverified. Historical coordinates cannot locate the current PDF."}
        </p>
      )}
      <button disabled={!current} onClick={() => onNavigate(e)}>
        {zh
          ? `打开 PDF 第 ${e.locator.page + 1} 页`
          : `Open PDF page ${e.locator.page + 1}`}
      </button>
    </div>
  );
}
