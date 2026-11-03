import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { lookupUpstreamModel, type ModelModality } from "../upstreamCatalog";

function ModalityGlyph({ modality }: { modality: ModelModality }) {
  const pen = {
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.6,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
  };
  let drawing: ReactNode;
  if (modality === "text") {
    drawing = (
      <>
        <path d="M3 4.5h10" {...pen} />
        <path d="M3 8h10" {...pen} />
        <path d="M3 11.5h6" {...pen} />
      </>
    );
  } else if (modality === "image") {
    drawing = (
      <>
        <rect x="2.2" y="3" width="11.6" height="10" rx="1.4" {...pen} />
        <circle cx="5.4" cy="6.1" r="1" fill="currentColor" stroke="none" />
        <path d="M3.4 12.2 6.4 8.8l2.1 2.1 1.8-1.8 2.3 3.1" {...pen} />
      </>
    );
  } else if (modality === "audio") {
    drawing = (
      <>
        <path d="M3.2 6.2v3.6h2.1l3 2.2V4L5.3 6.2H3.2z" {...pen} />
        <path d="M10.2 6.3a2.4 2.4 0 0 1 0 3.4" {...pen} />
        <path d="M11.8 4.8a4.2 4.2 0 0 1 0 6.4" {...pen} />
      </>
    );
  } else if (modality === "video") {
    drawing = (
      <>
        <rect x="2" y="3.2" width="12" height="9.6" rx="1.5" {...pen} />
        <path d="M7 6.1v3.8l3.3-1.9L7 6.1z" fill="currentColor" stroke="none" />
      </>
    );
  } else {
    drawing = (
      <>
        <path d="M4 2.6h5.2L12.2 5.6V13.2a.8.8 0 0 1-.8.8H4.8a.8.8 0 0 1-.8-.8V3.4a.8.8 0 0 1 .8-.8z" {...pen} />
        <path d="M9.1 2.8V5.6H12" {...pen} />
        <path d="M5.6 8.2h4.8M5.6 10.6h3.2" {...pen} />
      </>
    );
  }
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      {drawing}
    </svg>
  );
}

export function ModelModalityMarks({
  providerName,
  modelName,
  modelType,
  vision,
  compact = false,
}: {
  providerName: string;
  modelName: string;
  modelType?: string;
  vision?: boolean;
  compact?: boolean;
}) {
  const { t } = useTranslation();
  const model = lookupUpstreamModel(providerName, modelName);
  const normalizedType = modelType?.trim().toLowerCase();
  // VLM is intrinsically visual. For LLMs a saved false is an explicit
  // configuration decision, while absent metadata leaves the catalog intact.
  const imageInput = normalizedType === "vlm" || vision === true
    ? true
    : normalizedType === "llm" && vision === false ? false : undefined;
  if (!model && imageInput !== true) {
    return null;
  }
  let input: ModelModality[] = model ? [...model.input] : ["text"];
  if (imageInput === true && !input.includes("image")) input.push("image");
  else if (imageInput === false) input = input.filter((modality) => modality !== "image");
  const output: ModelModality[] = model?.output || ["text"];

  const renderGroup = (direction: "input" | "output", modalities: ModelModality[]) => (
    <span className="model-modality-group" aria-label={t(`modelProvider.modality.${direction}Aria`)}>
      <span className="model-modality-label">{t(`modelProvider.modality.${direction}`)}</span>
      {modalities.map((modality) => (
        <span
          className={`model-modality-logo is-${modality}`}
          key={`${direction}-${modality}`}
          title={t(`modelProvider.modality.${modality}`)}
          aria-label={t(`modelProvider.modality.${modality}`)}
        >
          <ModalityGlyph modality={modality} />
        </span>
      ))}
    </span>
  );

  return (
    <span className={`model-modality-marks${compact ? " is-compact" : ""}`}>
      {renderGroup("input", input)}
      {renderGroup("output", output)}
    </span>
  );
}
