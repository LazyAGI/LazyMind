import { RoleTypes } from "@/modules/chat/constants/common";

export const MAX_CITE_MESSAGE_COUNT = 3;

export function buildCitedMessageText(text: string, citeMessages?: string[]) {
  const normalizedText = text.trim();
  const normalizedCiteMessages =
    citeMessages?.map((item) => item.trim()).filter(Boolean) ?? [];
  if (normalizedCiteMessages.length < 1) {
    return normalizedText;
  }
  const citedText = normalizedCiteMessages
    .map((citeMessage) => `<cite_message>${citeMessage}</cite_message>`)
    .join("\n");
  return `${citedText}\n${normalizedText}`;
}

export function splitCiteMessages(citeMessage?: string) {
  return (citeMessage || "")
    .split(/\n{2,}/)
    .map((item) => item.trim())
    .filter(Boolean);
}

export function findLastUserMessageIndex(list: any[]): number {
  return list.reduce(
    (lastIndex, msg, idx) => (msg?.role === RoleTypes.USER ? idx : lastIndex),
    -1,
  );
}

export function getCiteMessages(message?: {
  cite_message?: string;
  cite_messages?: string[];
}) {
  if (Array.isArray(message?.cite_messages)) {
    return message.cite_messages.map((item) => item.trim()).filter(Boolean);
  }
  const textInput = (message as any)?.inputs?.find((input: any) => {
    const inputType = input?.input_type || "text";
    return inputType === "text" && typeof input?.text === "string";
  });
  const inputCites = Array.from(
    `${textInput?.text || ""}`.matchAll(/<cite_message>([\s\S]*?)<\/cite_message>/gi),
  )
    .map((match) => match[1]?.trim())
    .filter(Boolean);
  if (inputCites.length > 0) {
    return inputCites;
  }
  return splitCiteMessages(message?.cite_message);
}
