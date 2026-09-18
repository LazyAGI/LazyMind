import type { LearningCapability } from "./api";

export type CapabilityFamily = "explanation" | "translation" | "appreciation";

const FAMILY_MEMBERS: Record<CapabilityFamily, string[]> = {
  explanation: ["chinese_definition", "english_definition", "classical_definition", "pinyin"],
  translation: ["general_translation", "classical_translation"],
  appreciation: ["literary_appreciation"],
};

export const capabilityFamily = (key: string): CapabilityFamily => {
  if (FAMILY_MEMBERS.translation.includes(key)) return "translation";
  if (FAMILY_MEMBERS.appreciation.includes(key)) return "appreciation";
  return "explanation";
};

export const capabilityFamilyI18nKey = (family: CapabilityFamily) =>
  `learning.capabilityFamily.${family}`;

export const capabilityFamilies = (capabilities: LearningCapability[]) =>
  (["explanation", "translation", "appreciation"] as CapabilityFamily[])
    .filter((family) => capabilities.some((item) => capabilityFamily(item.key) === family));

export const capabilitiesForFamilies = (
  capabilities: LearningCapability[],
  families: CapabilityFamily[],
) => {
  const selected = capabilities.filter((item) => families.includes(capabilityFamily(item.key)));
  const explanationHasDefinition = selected.some((item) => capabilityFamily(item.key) === "explanation" && item.key !== "pinyin");
  return explanationHasDefinition ? selected.filter((item) => item.key !== "pinyin") : selected;
};

export const chooseFamilyCapability = (
  capabilities: LearningCapability[],
  family: CapabilityFamily,
  text: string,
) => {
  const candidates = capabilities.filter((item) => capabilityFamily(item.key) === family);
  const hasHan = /\p{Script=Han}/u.test(text);
  const hasLatin = /\p{Script=Latin}/u.test(text);
  const preferred = family === "explanation"
    ? hasLatin && !hasHan ? "english_definition" : "chinese_definition"
    : family === "translation" ? "general_translation" : "literary_appreciation";
  return candidates.find((item) => item.key === preferred)
    ?? candidates.find((item) => item.key !== "pinyin")
    ?? candidates[0];
};
