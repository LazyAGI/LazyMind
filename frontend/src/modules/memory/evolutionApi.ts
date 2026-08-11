import { axiosInstance, BASE_URL } from "@/components/request";

const evolutionSuggestionBasePath = `${BASE_URL}/api/core/evolution/suggestions`;

export interface EvolutionSuggestionRecord {
  id: string;
  action: string;
  category: string;
  content: string;
  createdAt: string;
  fileExt: string;
  fullContent: string;
  invalidReason: string;
  outdated: boolean;
  parentSkillName: string;
  reason: string;
  relativePath: string;
  resourceKey: string;
  resourceType: string;
  reviewedAt?: string;
  reviewerId: string;
  reviewerName: string;
  sessionId: string;
  skillName: string;
  status: string;
  title: string;
  updatedAt: string;
  userId: string;
}

export async function approveEvolutionSuggestion(
  suggestionId: string,
): Promise<void> {
  await axiosInstance.post(
    `${evolutionSuggestionBasePath}/${encodeURIComponent(suggestionId)}:approve`,
  );
}

export async function rejectEvolutionSuggestion(
  suggestionId: string,
): Promise<void> {
  await axiosInstance.post(
    `${evolutionSuggestionBasePath}/${encodeURIComponent(suggestionId)}:reject`,
  );
}

const submitEvolutionSuggestionBatchDecision = async (
  action: "batchApprove" | "batchReject",
  suggestionIds: string[],
): Promise<void> => {
  const ids = Array.from(
    new Set(suggestionIds.map((item) => item.trim()).filter(Boolean)),
  );
  if (!ids.length) {
    return;
  }

  await axiosInstance.post(`${evolutionSuggestionBasePath}:${action}`, { ids });
};

export async function batchApproveEvolutionSuggestions(
  suggestionIds: string[],
): Promise<void> {
  await submitEvolutionSuggestionBatchDecision("batchApprove", suggestionIds);
}

export async function batchRejectEvolutionSuggestions(
  suggestionIds: string[],
): Promise<void> {
  await submitEvolutionSuggestionBatchDecision("batchReject", suggestionIds);
}
