export type PersonalizationEvolutionState =
  | "off"
  | "waiting"
  | "pending_review"
  | "applying"
  | "applied"
  | "failed";

export interface PersonalizationEvolutionSource {
  autoEvo?: boolean;
  autoEvoApplyStatus?: string;
  autoEvoError?: string;
  autoEvoStartedAt?: string;
  autoEvoFinishedAt?: string;
  hasPendingReviewSuggestions?: boolean;
  reviewStatus?: string;
}

export interface PersonalizationEvolutionProjection {
  state: PersonalizationEvolutionState;
  error: string;
  latestAt: string;
}

const normalized = (value?: string) => String(value || "").trim().toLowerCase();

export const formatPersonalizationEvolutionTime = (value?: string): string => {
  if (!value) return "";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "" : parsed.toLocaleString();
};

export const projectPersonalizationEvolutionState = (
  source: PersonalizationEvolutionSource,
): PersonalizationEvolutionProjection => {
  if (!source.autoEvo) {
    return { state: "off", error: "", latestAt: source.autoEvoFinishedAt || "" };
  }

  const applyStatus = normalized(source.autoEvoApplyStatus);
  const error = String(source.autoEvoError || "").trim();
  const latestAt = source.autoEvoFinishedAt || source.autoEvoStartedAt || "";
  if (applyStatus === "failed" || error) {
    return { state: "failed", error, latestAt };
  }
  if (applyStatus === "running") {
    return { state: "applying", error: "", latestAt };
  }
  if (
    source.hasPendingReviewSuggestions ||
    normalized(source.reviewStatus) === "pending"
  ) {
    return { state: "pending_review", error: "", latestAt };
  }
  if (source.autoEvoFinishedAt) {
    return { state: "applied", error: "", latestAt };
  }
  return { state: "waiting", error: "", latestAt };
};
