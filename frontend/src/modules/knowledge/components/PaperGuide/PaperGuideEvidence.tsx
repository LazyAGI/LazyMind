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
      {current && e.locator.kind === "page" && <p>{zh ? "已核实物理页；仅定位到页面，不显示精确文字框。" : "Verified physical page; page-only location without an exact text box."}</p>}
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
            ? "源文件或来源定位已更新，或尚未核对；旧导读仍可阅读，历史坐标不能定位当前 PDF。"
            : "Source content or positioning was updated, or is unverified. The old guide remains readable; historical coordinates cannot locate the current PDF."}
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
