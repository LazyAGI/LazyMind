import { describe, expect, it } from "vitest";
import {
  buildDiffLines,
  buildDiffLinesWithInline,
  buildExperienceProposalFromSuggestions,
  buildSkillProposalFromSuggestions,
  buildUnifiedDiffLines,
  canUploadSkillFile,
  cloneExperienceAsset,
  cloneGlossaryAsset,
  cloneStructuredAsset,
  createChildSkillDraft,
  createDraft,
  createId,
  createStructuredDraft,
  formatDateTime,
  getBaseName,
  getPreferenceSuggestionResourceParam,
  getSkillBodyContentForDisplay,
  getSkillSuggestionResourceParam,
  inferSkillFileExt,
  isMarkdownSkillFile,
  isSkillShareActionable,
  isSkillUpdatePending,
  isSkillUpdatePendingForRecord,
  normalizeSuggestionValue,
  normalizeTagValues,
  normalizeTextValues,
  parseChangeProposalTab,
  parseMarkdownFrontMatter,
  parseMemoryTab,
  parsePreferenceYamlAndBody,
  serializeExperienceAsset,
  serializePreferenceYaml,
  serializeStructuredAsset,
  splitMarkdownFrontMatter,
  type ExperienceAsset,
  type StructuredAsset,
} from "./shared";
import type { EvolutionSuggestionRecord } from "./preferenceApi";

const buildStructuredAsset = (overrides: Partial<StructuredAsset> = {}): StructuredAsset => ({
  id: "skill-1",
  name: "示例技能",
  description: "示例描述",
  category: "分类",
  tags: ["标签A"],
  content: "内容",
  protect: false,
  ...overrides,
});

const buildExperienceAsset = (overrides: Partial<ExperienceAsset> = {}): ExperienceAsset => ({
  id: "exp-1",
  title: "标题",
  content: "内容",
  protect: false,
  ...overrides,
});

describe("isSkillShareActionable", () => {
  it("is actionable for pending and unknown statuses", () => {
    expect(isSkillShareActionable("pending")).toBe(true);
    expect(isSkillShareActionable("unknown")).toBe(true);
  });

  it("is not actionable for accepted/rejected/failed", () => {
    expect(isSkillShareActionable("accepted")).toBe(false);
    expect(isSkillShareActionable("rejected")).toBe(false);
    expect(isSkillShareActionable("failed")).toBe(false);
  });
});

describe("isSkillUpdatePending / isSkillUpdatePendingForRecord", () => {
  it("normalizes case and whitespace before comparing", () => {
    expect(isSkillUpdatePending("  Pending ")).toBe(true);
    expect(isSkillUpdatePending("done")).toBe(false);
    expect(isSkillUpdatePending(undefined)).toBe(false);
  });

  it("flags a record pending when any pending-related flag is set", () => {
    expect(
      isSkillUpdatePendingForRecord(buildStructuredAsset({ hasPendingReviewResult: true })),
    ).toBe(true);
    expect(
      isSkillUpdatePendingForRecord(
        buildStructuredAsset({ hasPendingReviewSuggestions: true }),
      ),
    ).toBe(true);
    expect(isSkillUpdatePendingForRecord(buildStructuredAsset())).toBe(false);
  });
});

describe("formatDateTime", () => {
  it("formats a valid date string", () => {
    expect(formatDateTime("2024-01-02T03:04:05Z")).toMatch(/2024-01-02/);
  });

  it("returns a dash for empty input", () => {
    expect(formatDateTime(undefined)).toBe("-");
    expect(formatDateTime("")).toBe("-");
  });

  it("returns the raw value when parsing fails", () => {
    expect(formatDateTime("not-a-date")).toBe("not-a-date");
  });
});

describe("createDraft / createStructuredDraft", () => {
  it("creates an empty draft with expected defaults", () => {
    const draft = createDraft();
    expect(draft.source).toBe("user");
    expect(draft.tags).toEqual([]);
    expect(draft.protect).toBe(false);
  });

  it("builds a structured draft from an existing item", () => {
    const item = buildStructuredAsset({ protect: true });
    const draft = createStructuredDraft(item);
    expect(draft.name).toBe(item.name);
    expect(draft.protect).toBe(true);
    expect(draft.content).toBe(item.content);
  });

  it("strips front matter from content when requested", () => {
    const item = buildStructuredAsset({
      content: "---\nname: foo\n---\nbody text",
    });
    const draft = createStructuredDraft(item, { stripFrontMatter: true });
    expect(draft.content).toBe("body text");
  });
});

describe("createId / createChildSkillDraft", () => {
  it("creates a unique id prefixed with the given string", () => {
    const id = createId("child");
    expect(id.startsWith("child-")).toBe(true);
  });

  it("creates an empty child skill draft with a tempId", () => {
    const draft = createChildSkillDraft();
    expect(draft.tempId.startsWith("child-skill-")).toBe(true);
    expect(draft.name).toBe("");
    expect(draft.tags).toEqual([]);
  });
});

describe("getBaseName / canUploadSkillFile / isMarkdownSkillFile", () => {
  it("strips the extension from a filename", () => {
    expect(getBaseName("SKILL.md")).toBe("SKILL");
    expect(getBaseName("archive.tar.gz")).toBe("archive.tar");
  });

  it("accepts allowed suffixes for general upload", () => {
    expect(canUploadSkillFile("notes.txt")).toBe(true);
    expect(canUploadSkillFile("data.yaml")).toBe(true);
    expect(canUploadSkillFile("image.png")).toBe(false);
  });

  it("restricts to markdown suffixes when parentOnly is set", () => {
    expect(canUploadSkillFile("notes.txt", true)).toBe(false);
    expect(canUploadSkillFile("SKILL.md", true)).toBe(true);
  });

  it("detects markdown files case-insensitively", () => {
    expect(isMarkdownSkillFile("SKILL.MD")).toBe(true);
    expect(isMarkdownSkillFile("doc.markdown")).toBe(true);
    expect(isMarkdownSkillFile("doc.txt")).toBe(false);
  });
});

describe("splitMarkdownFrontMatter / parseMarkdownFrontMatter", () => {
  it("splits front matter and body", () => {
    const content = "---\nname: foo\ndescription: bar\n---\nbody line";
    const split = splitMarkdownFrontMatter(content);
    expect(split?.content).toBe("body line");
    expect(split?.fields).toContain("name: foo");
  });

  it("returns null when there is no front matter block", () => {
    expect(splitMarkdownFrontMatter("just body text")).toBeNull();
  });

  it("parses fields into a metadata object", () => {
    const parsed = parseMarkdownFrontMatter(
      "---\nname: foo\ndescription: bar desc\ncategory: cat\n---\nbody",
    );
    expect(parsed).toEqual({ name: "foo", description: "bar desc", category: "cat", content: "body" });
  });

  it("returns null when content has no front matter", () => {
    expect(parseMarkdownFrontMatter("no front matter here")).toBeNull();
  });
});

describe("getSkillBodyContentForDisplay", () => {
  it("returns empty string for empty content", () => {
    expect(getSkillBodyContentForDisplay("")).toBe("");
  });

  it("strips front matter and leading meta/divider lines", () => {
    const content = "---\nname: foo\n---\n**name**: foo\n---\n\nActual body content";
    expect(getSkillBodyContentForDisplay(content)).toBe("Actual body content");
  });

  it("collapses excessive blank lines", () => {
    const content = "line1\n\n\n\n\nline2";
    expect(getSkillBodyContentForDisplay(content)).toBe("line1\n\nline2");
  });
});

describe("inferSkillFileExt", () => {
  it("infers extension from filename, normalizing markdown to md", () => {
    expect(inferSkillFileExt("notes.markdown")).toBe("md");
    expect(inferSkillFileExt("data.json")).toBe("json");
  });

  it("infers json from content when filename is missing", () => {
    expect(inferSkillFileExt(undefined, '{"a":1}')).toBe("json");
    expect(inferSkillFileExt(undefined, "[1,2]")).toBe("json");
  });

  it("defaults to md when nothing else matches", () => {
    expect(inferSkillFileExt(undefined, "plain text")).toBe("md");
    expect(inferSkillFileExt("file.unknownext", "plain text")).toBe("md");
  });
});

describe("normalizeTagValues / normalizeTextValues", () => {
  it("trims, dedupes, and filters empty values", () => {
    expect(normalizeTagValues([" a ", "a", "", "b"])).toEqual(["a", "b"]);
    expect(normalizeTextValues([" x ", "x", ""])).toEqual(["x"]);
  });
});

describe("cloneStructuredAsset / cloneExperienceAsset / cloneGlossaryAsset", () => {
  it("deep clones the array fields so mutation does not affect the source", () => {
    const skill = buildStructuredAsset();
    const cloned = cloneStructuredAsset(skill);
    cloned.tags.push("new-tag");
    expect(skill.tags).toEqual(["标签A"]);
  });

  it("clones experience assets as a shallow copy", () => {
    const experience = buildExperienceAsset();
    const cloned = cloneExperienceAsset(experience);
    expect(cloned).toEqual(experience);
    expect(cloned).not.toBe(experience);
  });

  it("deep clones glossary aliases", () => {
    const glossary = { id: "g1", term: "t", group: "g", aliases: ["a1"], source: "user" as const, content: "c" };
    const cloned = cloneGlossaryAsset(glossary);
    cloned.aliases.push("a2");
    expect(glossary.aliases).toEqual(["a1"]);
  });
});

describe("serializeStructuredAsset / serializeExperienceAsset", () => {
  const labels = {
    name: "名称",
    description: "描述",
    category: "分类",
    tags: "标签",
    protect: "保护",
    content: "内容",
    yes: "是",
    no: "否",
  };

  it("serializes structured asset fields into readable lines", () => {
    const text = serializeStructuredAsset(buildStructuredAsset({ protect: true }), labels);
    expect(text).toContain("名称: 示例技能");
    expect(text).toContain("保护: 是");
    expect(text).toContain("标签A");
  });

  it("uses a dash placeholder when tags are empty", () => {
    const text = serializeStructuredAsset(buildStructuredAsset({ tags: [] }), labels);
    expect(text).toContain("标签: -");
  });

  it("serializes experience asset fields", () => {
    const text = serializeExperienceAsset(buildExperienceAsset({ protect: false }), {
      title: "标题",
      protect: "保护",
      content: "内容",
      yes: "是",
      no: "否",
    });
    expect(text).toContain("标题: 标题");
    expect(text).toContain("保护: 否");
  });
});

describe("buildDiffLines", () => {
  it("marks added, removed, and unchanged lines", () => {
    const lines = buildDiffLines("line1\nline2", "line1\nline3");
    const types = lines.map((line) => line.type);
    expect(types).toContain("remove");
    expect(types).toContain("add");
    expect(types).toContain("same");
  });

  it("returns no lines for identical text", () => {
    const lines = buildDiffLines("same text", "same text");
    expect(lines.every((line) => line.type === "same")).toBe(true);
  });
});

describe("buildDiffLinesWithInline", () => {
  it("pairs similar remove/add lines with inline spans", () => {
    const lines = buildDiffLinesWithInline("hello world", "hello there");
    const removeLine = lines.find((line) => line.type === "remove");
    const addLine = lines.find((line) => line.type === "add");
    expect(removeLine?.inlineSpans).toBeDefined();
    expect(addLine?.inlineSpans).toBeDefined();
  });

  it("does not pair dissimilar blocks", () => {
    const lines = buildDiffLinesWithInline("abc", "xyz123456789");
    const removeLine = lines.find((line) => line.type === "remove");
    expect(removeLine?.inlineSpans).toBeUndefined();
  });
});

describe("buildUnifiedDiffLines", () => {
  it("parses unified diff prefixes into typed lines", () => {
    const diffText = "+++ b/file\n--- a/file\n+added line\n-removed line\n unchanged line";
    const lines = buildUnifiedDiffLines(diffText);
    expect(lines).toEqual([
      { type: "same", text: "+++ b/file" },
      { type: "same", text: "--- a/file" },
      { type: "add", text: "added line" },
      { type: "remove", text: "removed line" },
      { type: "same", text: "unchanged line" },
    ]);
  });

  it("drops a trailing empty line from a trailing newline", () => {
    const lines = buildUnifiedDiffLines("+added\n");
    expect(lines).toEqual([{ type: "add", text: "added" }]);
  });
});

describe("normalizeSuggestionValue", () => {
  it("collapses whitespace and returns a dash for empty content", () => {
    expect(normalizeSuggestionValue("  a   b  ")).toBe("a b");
    expect(normalizeSuggestionValue("   ")).toBe("-");
  });

  it("truncates long values with an ellipsis", () => {
    const longValue = "x".repeat(150);
    const result = normalizeSuggestionValue(longValue);
    expect(result.endsWith("...")).toBe(true);
    expect(result.length).toBe(123);
  });
});

describe("parsePreferenceYamlAndBody", () => {
  it("splits yaml frontmatter and body text", () => {
    const content = '---\nagent_persona: "a"\n---\nbody text';
    const { yamlText, bodyText } = parsePreferenceYamlAndBody(content);
    expect(yamlText).toBe('---\nagent_persona: "a"\n---');
    expect(bodyText).toBe("body text");
  });

  it("treats the whole content as body when there is no frontmatter", () => {
    const { yamlText, bodyText } = parsePreferenceYamlAndBody("just body");
    expect(yamlText).toBe("");
    expect(bodyText).toBe("just body");
  });
});

describe("serializePreferenceYaml", () => {
  it("serializes the structured preference fields", () => {
    const yaml = serializePreferenceYaml({
      agentPersona: "persona",
      preferredName: "name",
      responseStyle: "style",
    });
    expect(yaml).toContain('agent_persona: "persona"');
    expect(yaml).toContain('preferred_name: "name"');
    expect(yaml).toContain('response_style: "style"');
  });

  it("defaults missing fields to empty strings", () => {
    const yaml = serializePreferenceYaml({});
    expect(yaml).toContain('agent_persona: ""');
  });
});

describe("getPreferenceSuggestionResourceParam", () => {
  it("routes skill resource types to skill evolution ids", () => {
    const result = getPreferenceSuggestionResourceParam(
      buildExperienceAsset({ resourceType: "skill" }),
    );
    expect(result.evolutionId).toBe("skill:exp-1");
  });

  it("routes memory (non-preference) types to memory evolution ids", () => {
    const result = getPreferenceSuggestionResourceParam(
      buildExperienceAsset({ resourceType: "memory" }),
    );
    expect(result.evolutionId).toBe("memory:exp-1");
  });

  it("defaults to user-preference for anything else", () => {
    const result = getPreferenceSuggestionResourceParam(buildExperienceAsset());
    expect(result.evolutionId).toBe("user-preference:exp-1");
  });
});

describe("getSkillSuggestionResourceParam", () => {
  it("builds a skill-prefixed evolution id", () => {
    expect(getSkillSuggestionResourceParam(buildStructuredAsset()).evolutionId).toBe(
      "skill:skill-1",
    );
  });
});

const buildSuggestion = (id: string): EvolutionSuggestionRecord => ({
  id,
  action: "update",
  category: "",
  content: "",
  createdAt: "",
  fileExt: "md",
  fullContent: "",
  invalidReason: "",
  outdated: false,
  parentSkillName: "",
  reason: "",
  relativePath: "",
  resourceKey: "",
  resourceType: "skill",
  reviewerId: "",
  reviewerName: "",
  sessionId: "",
  skillName: "",
  status: "pending",
  title: "",
  updatedAt: "",
  userId: "",
});

describe("buildSkillProposalFromSuggestions / buildExperienceProposalFromSuggestions", () => {
  it("returns null when there are no suggestions", () => {
    expect(buildSkillProposalFromSuggestions(buildStructuredAsset(), [])).toBeNull();
    expect(buildExperienceProposalFromSuggestions(buildExperienceAsset(), [])).toBeNull();
  });

  it("builds a skill proposal referencing the first suggestion", () => {
    const suggestions = [buildSuggestion("s1"), buildSuggestion("s2")];
    const proposal = buildSkillProposalFromSuggestions(buildStructuredAsset(), suggestions, {
      page: 1,
      pageSize: 10,
      total: 2,
    });
    expect(proposal?.backendSuggestionId).toBe("s1");
    expect(proposal?.backendSuggestionTotal).toBe(2);
    expect(proposal?.tab).toBe("skills");
  });

  it("builds an experience proposal defaulting total to suggestion count", () => {
    const suggestions = [buildSuggestion("s1")];
    const proposal = buildExperienceProposalFromSuggestions(buildExperienceAsset(), suggestions);
    expect(proposal?.tab).toBe("experience");
    expect(proposal?.backendSuggestionTotal).toBe(1);
  });
});

describe("parseMemoryTab / parseChangeProposalTab", () => {
  it("accepts only the recognized memory tab values", () => {
    expect(parseMemoryTab("skills")).toBe("skills");
    expect(parseMemoryTab("glossary")).toBe("glossary");
    expect(parseMemoryTab("unknown")).toBeNull();
    expect(parseMemoryTab(null)).toBeNull();
  });

  it("accepts only skills/experience for change proposal tabs", () => {
    expect(parseChangeProposalTab("skills")).toBe("skills");
    expect(parseChangeProposalTab("experience")).toBe("experience");
    expect(parseChangeProposalTab("glossary")).toBeNull();
  });
});
