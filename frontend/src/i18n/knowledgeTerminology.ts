import type { PostProcessorModule } from "i18next";

const KNOWLEDGE_BASE_TERM = "知识库";
const LIBRARY_TERM = "资料库";
const DEVELOPER_ACTIVE_STORAGE_KEY = "lazymind:developer-active";
const DEVELOPER_ACTIVE_EVENT = "lazymind:developer-active-change";

function isDeveloperModeActive() {
  try {
    return localStorage.getItem(DEVELOPER_ACTIVE_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function applyKnowledgeTerminology(
  value: string,
  language: string | undefined,
  developerActive: boolean,
) {
  if (developerActive || !language?.toLowerCase().startsWith("zh")) {
    return value;
  }
  return value.split(KNOWLEDGE_BASE_TERM).join(LIBRARY_TERM);
}

export const knowledgeTerminologyPostProcessor: PostProcessorModule = {
  type: "postProcessor",
  name: "knowledgeTerminology",
  process(value, _key, _options, translator) {
    return applyKnowledgeTerminology(
      value,
      translator.language,
      isDeveloperModeActive(),
    );
  },
};

export function subscribeKnowledgeTerminologyChange(onChange: () => void) {
  const handleStorage = (event: StorageEvent) => {
    if (event.key === DEVELOPER_ACTIVE_STORAGE_KEY) {
      onChange();
    }
  };
  window.addEventListener(DEVELOPER_ACTIVE_EVENT, onChange);
  window.addEventListener("storage", handleStorage);
  return () => {
    window.removeEventListener(DEVELOPER_ACTIVE_EVENT, onChange);
    window.removeEventListener("storage", handleStorage);
  };
}
