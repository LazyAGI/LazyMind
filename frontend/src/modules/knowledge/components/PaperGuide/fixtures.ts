import type {
  PaperGuidePublishedSection,
  PaperGuideRevisionResponse,
  PaperGuideStatusResponse,
} from "@/api/generated/core-client";
const titles: Record<string, string[]> = {
  method: [
    "A difficult recognition problem",
    "Why local features fail",
    "Learning contextual representations",
    "Comparisons under matched conditions",
  ],
  theory: [
    "A question of convergence",
    "Which assumptions are needed",
    "A bounded convergence theorem",
    "Proof and counterexample",
  ],
  normative: [
    "Public reasoning in context",
    "Who can justify authority",
    "A procedural account of legitimacy",
    "Objections and interpretive limits",
  ],
  review: [
    "A fragmented literature",
    "How the field is organized",
    "A taxonomy of approaches",
    "Coverage and unresolved disagreement",
  ],
};
export function section(
  role: string,
  title = role,
): PaperGuidePublishedSection {
  return {
    key: `section:${role}:v1`,
    role,
    title,
    summary: "",
    version: 1,
    change_version: 1,
    publication_status: "published",
    claim_ids: [],
    claims: [],
    blocks: [
      {
        id: `${role}-block`,
        kind: role === "contribution" ? "example" : "paragraph",
        attribution: role === "contribution" ? "teaching" : "paper_statement",
        text: "甲😀乙. A concrete explanation of the argument.",
        claim_ids: [],
        evidence_ids: role === "background" ? ["e1"] : [],
      },
    ],
  };
}
export function revision(profile = "method"): PaperGuideRevisionResponse {
  return {
    id: "r1",
    status: "complete",
    version: 1,
    source_sha256: "hash",
    ir_digest: "ir",
    source_current: true,
    created_at: "2026-11-15T00:00:00Z",
    model: {
      id: "model",
      name: "Study model",
      ref: "own:model",
      protocol: "openai",
      parameters_hash: "p",
      context_tokens: 32000,
    },
    run: {
      id: "r1",
      stage: "completed",
      job_id: "j1",
      active_millis: 1,
      calls_used: 10,
      call_limit: 24,
      cost_known: false,
    },
    coverage: {
      abstract_only: false,
      body: true,
      appendix: false,
      conclusion: true,
      limitations: true,
      references: true,
      page_warnings: [],
      warnings: [],
    },
    sections: ["background", "question", "contribution", "justification"].map(
      (r, i) => section(r, titles[profile][i]),
    ),
  };
}
export function status(): PaperGuideStatusResponse {
  return {
    enabled: true,
    can_read: true,
    can_generate: true,
    source_sha256: "hash",
    version: 1,
    published_revision: revision(),
    candidate_revision: null,
  };
}
