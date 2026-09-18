import { expect, it } from "vitest";
import type { LearningCapability } from "./api";
import { capabilitiesForFamilies, capabilityFamilies, capabilityFamily, chooseFamilyCapability } from "./capabilityFamilies";

const capability = (key: string): LearningCapability => ({
  key,
  version: 1,
  name_i18n_key: key,
  description_i18n_key: key,
  local_only: true,
  languages: [],
  subject_kinds: [],
  fields: [],
  provider_pipeline: [],
  allowed_question_types: [],
  default_question_types: [],
  cache_policy: { default_scope: "document", allowed_scopes: ["document"], context_sensitive: true },
});

const catalog = [
  capability("chinese_definition"),
  capability("english_definition"),
  capability("pinyin"),
  capability("general_translation"),
  capability("classical_translation"),
  capability("literary_appreciation"),
];

it("maps legacy providers into the three public capability families", () => {
  expect(capabilityFamily("classical_definition")).toBe("explanation");
  expect(capabilityFamily("classical_translation")).toBe("translation");
  expect(capabilityFamilies(catalog)).toEqual(["explanation", "translation", "appreciation"]);
});

it("does not generate pinyin as a standalone task when an explanation provider exists", () => {
  expect(capabilitiesForFamilies(catalog, ["explanation"]).map((item) => item.key)).toEqual([
    "chinese_definition",
    "english_definition",
  ]);
});

it("routes explanation to the language-appropriate provider", () => {
  expect(chooseFamilyCapability(catalog, "explanation", "仙人指路")?.key).toBe("chinese_definition");
  expect(chooseFamilyCapability(catalog, "explanation", "stream reader")?.key).toBe("english_definition");
});
