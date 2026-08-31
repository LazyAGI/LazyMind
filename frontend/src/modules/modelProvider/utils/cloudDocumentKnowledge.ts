import type { SourceType } from "@/modules/dataSource/constants/types";

export type CloudKnowledgeProvider = Extract<
  SourceType,
  "local" | "feishu" | "notion"
>;

const CLOUD_KNOWLEDGE_CREATE_PATH = "/lib/knowledge/list";
const CLOUD_KNOWLEDGE_CREATE_SOURCE = "cloud-documents";
const CLOUD_KNOWLEDGE_PROVIDERS = new Set<CloudKnowledgeProvider>([
  "local",
  "feishu",
  "notion",
]);

export function getCloudKnowledgeCreatePath(provider?: CloudKnowledgeProvider) {
  const params = new URLSearchParams();
  params.set("createSource", CLOUD_KNOWLEDGE_CREATE_SOURCE);
  if (provider) {
    params.set("provider", provider);
  }
  return `${CLOUD_KNOWLEDGE_CREATE_PATH}?${params.toString()}`;
}

export function getCloudKnowledgeCreateProvider(search: string) {
  const params = new URLSearchParams(search);
  if (!isCloudKnowledgeCreateRequest(search)) {
    return null;
  }
  const provider = params.get("provider");
  return CLOUD_KNOWLEDGE_PROVIDERS.has(provider as CloudKnowledgeProvider)
    ? (provider as CloudKnowledgeProvider)
    : null;
}

export function isCloudKnowledgeCreateRequest(search: string) {
  return (
    new URLSearchParams(search).get("createSource") ===
    CLOUD_KNOWLEDGE_CREATE_SOURCE
  );
}

export function clearCloudKnowledgeCreateParams(search: string) {
  const params = new URLSearchParams(search);
  params.delete("createSource");
  params.delete("provider");
  const nextSearch = params.toString();
  return nextSearch ? `?${nextSearch}` : "";
}
