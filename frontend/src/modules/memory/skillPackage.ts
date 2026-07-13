import JSZip from "@progress/jszip-esm";

const SKILL_MD_PATH = "SKILL.md";

export const buildSkillMarkdownContent = (options: {
  name: string;
  description: string;
  body: string;
}) => {
  const name = options.name.trim();
  const description = options.description.trim();
  const body = options.body.trim();

  const frontMatter = [
    "---",
    `name: ${name}`,
    description ? `description: ${description}` : "",
    "---",
  ]
    .filter(Boolean)
    .join("\n");

  return `${frontMatter}\n\n${body}`.trim();
};

export async function buildSkillZipBlob(options: {
  name: string;
  description: string;
  body: string;
  filename?: string;
}): Promise<File> {
  const zip = new JSZip();
  const markdown = buildSkillMarkdownContent(options);
  zip.file(SKILL_MD_PATH, markdown);

  const blob = await zip.generateAsync({ type: "blob" });
  const safeName = (options.filename || options.name || "skill")
    .trim()
    .replace(/[^\w.-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 80);

  return new File([blob], `${safeName || "skill"}.zip`, {
    type: "application/zip",
  });
}
