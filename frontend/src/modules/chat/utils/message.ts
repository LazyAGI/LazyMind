import type { Query } from "@/api/generated/chatbot-client";

const CITE_MESSAGE_PATTERN =
  /<cite_message>([\s\S]*?)<\/cite_message>\s*/i;
const CITE_MESSAGE_GLOBAL_PATTERN =
  /<cite_message>([\s\S]*?)<\/cite_message>\s*/gi;

interface ChatUserMessageLike {
  delta?: string;
  inputs?: Query[] | null;
}

export function normalizeMessageInputs(
  inputs?: Query[] | null,
  fallbackText?: string,
): Query[] {
  const normalizedInputs = Array.isArray(inputs)
    ? inputs
        .filter((item): item is Query => !!item)
        .map((item) => ({ ...item }))
    : [];

  const trimmedFallbackText = fallbackText?.trim();
  const hasTextInput = normalizedInputs.some((item) => {
    const inputType = item.input_type || "text";
    return inputType === "text" && !!item.text?.trim();
  });

  if (!hasTextInput && trimmedFallbackText) {
    normalizedInputs.unshift({
      input_type: "text",
      text: fallbackText,
    });
  }

  return normalizedInputs;
}

export function getRegenerationInputs(
  userMessage?: ChatUserMessageLike,
): Query[] {
  if (!userMessage) {
    return [];
  }

  return normalizeMessageInputs(userMessage.inputs, userMessage.delta);
}

export function getCitationFromText(text?: string) {
  return text?.match(CITE_MESSAGE_PATTERN)?.[1]?.trim() || "";
}

export function stripCitationFromText(text?: string) {
  return (text || "").replace(CITE_MESSAGE_GLOBAL_PATTERN, "").trim();
}
