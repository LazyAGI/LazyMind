import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const sourceDir = path.resolve(scriptDir, "../../src");

const forbiddenPatterns = [
  {
    label: "raw backend message in a toast",
    pattern:
      /message\.(?:error|warning)\(\s*(?:error|err|result|response|payload)(?:\?\.)?\.message/gi,
  },
  {
    label: "raw backend message used as a toast fallback",
    pattern:
      /message\.(?:error|warning)\([\s\S]{0,160}(?:error|err|result|response|payload)(?:\?\.)?\.message\s*\|\|/gi,
  },
  {
    label: "raw backend error/message in a user-visible expression",
    pattern:
      /(?:message\.(?:error|warning|info)\([\s\S]{0,220}?|(?:description|content|title|text)\s*=\s*\{[^}\n]{0,220})\b(?:response|payload|result|error|err)(?:\?\.)?(?:data(?:\?\.)?)?\.(?:error|message)\b/gi,
  },
  {
    label: "raw Error.message assigned to user-visible error state",
    pattern:
      /\bset[A-Z]\w*(?:Error|Message)\(\s*(?:error|err|response|payload|result)(?:\?\.)[^\s;(),]*\bmessage\b/gi,
  },
  {
    label: "raw backend diagnostic field rendered in JSX",
    pattern:
      /(?:description|content|message|text)\s*=\s*\{[^}\n]{0,240}\b(?:draft|run|item)\.(?:message|reason|generate_error|generate_warning)\b(?!\s*\.(?:startsWith|includes|trim)\b)/gi,
  },
  {
    label: "raw OAuth error description rendered in UI state",
    pattern:
      /(?:subtitle|description|content)\s*[:=]\s*\{?\s*errorDescription\b/gi,
  },
  {
    label: "raw OAuth error description forwarded through a UI message variable",
    pattern:
      /\b(?:const|let|var)\s+message\s*=\s*errorDescription\b[\s\S]{0,260}(?:subtitle|description|content)\s*:\s*message\b/gi,
  },
  {
    label: "raw error field rendered from a failed-status branch",
    pattern:
      /(?:status|generate_status|sync_state)\s*(?:===|==)\s*["'`](?:FAILED|failed|ERROR|error|rejected)["'`][\s\S]{0,500}(?:description|content|message|text)\s*=\s*\{[^}]*\b(?:last_error|error_message|generate_error|generate_warning)\b/gi,
  },
  {
    label: "HTTP status exposed as an Error message",
    pattern:
      /(?:throw new Error|message\.(?:error|warning)|set[A-Z]\w*(?:Error|Message))\([\s\S]{0,220}\$\{[^}]*\bstatus\b/gi,
  },
];

const userVisibleErrorFields = /\b(?:last_error|last_check_error|error_message|err_msg|generate_error|generate_warning)\b/;
const declarationOnly = /^\s*(?:export\s+)?(?:interface|type)\b|^\s*(?:readonly\s+)?(?:last_error|last_check_error|error_message|err_msg|generate_error|generate_warning)\??\s*:/;

function collectUserVisibleErrorFieldMatches(source) {
  const matches = [];
  const lines = source.split("\n");
  let offset = 0;
  for (const [lineIndex, line] of lines.entries()) {
    const lineOffset = offset;
    offset += line.length + 1;
    if (line.trimStart().startsWith("//")) continue;
    if (declarationOnly.test(line)) continue;
    const field = userVisibleErrorFields.source;
    for (const fieldMatch of line.matchAll(new RegExp(field, "g"))) {
      const fieldIndex = lineOffset + fieldMatch.index;
      const before = source.slice(Math.max(0, fieldIndex - 260), fieldIndex);
      const after = source.slice(fieldIndex, fieldIndex + 260);
      const visibleBefore = new RegExp(
        `(?:description|dataIndex|message\\.(?:error|warning|info)|set[A-Z]\\w*(?:Error|Message))[\\s\\S]{0,180}$`,
      ).test(before) || /\bt\s*\([\s\S]{0,220}?\{\s*(?:error|message|detail)\s*:[\s\S]{0,220}$/.test(before);
      if (!visibleBefore) continue;
      if (/localizeErrorCode|getCatalogErrorOrValue|getFriendlyFailureReason/.test(`${before}${after}`)) continue;
      matches.push({ index: fieldIndex, label: "raw backend error field in a user-visible expression" });
    }
  }
  return matches;
}

function collectCatchHardcodedMatches(source) {
  const matches = [];
  const visibleLiteral = /(?:message\.(?:error|warning|info)|set[A-Z]\w*(?:Error|Message))\(\s*(["'`])[^"'`]*(?:failed|failure|error|失败|错误|出错|异常)[^"'`]*\1/gi;
  for (const match of source.matchAll(visibleLiteral)) {
    const prefix = source.slice(0, match.index);
    const catchIndex = prefix.lastIndexOf("catch");
    if (catchIndex < 0) continue;
    const between = prefix.slice(catchIndex, match.index);
    const opens = (between.match(/\{/g) || []).length;
    const closes = (between.match(/\}/g) || []).length;
    if (opens <= closes) continue;
    matches.push({ index: match.index, label: "hardcoded failure text in an interface catch" });
  }

  // Page-specific translated failure keys tend to hide the same bypass behind
  // t(...). Keep this limited to catch blocks and exempt client-only validation,
  // copy, and local-file parsing messages.
  const translatedLiteral = /(?:message\.(?:error|warning|info)|set[A-Z]\w*(?:Error|Message))\(\s*(?:t|i18n\.t)\(\s*(["'`])([^"'`]+)\1/gi;
  const frontendOnlyKey = /(?:copyFailed|Error(?:Empty|Invalid)|Invalid|TypeError|unsupported|parseFailed(?:$|\.)|fileFormat|noFile|noRows|memoryUploadSkillFailed|memoryPreferenceDraftPreviewFailed|template\.downloadFailed)/i;
  for (const match of source.matchAll(translatedLiteral)) {
    if (!/(?:failed|failure|error|失败|错误|出错|异常)/i.test(match[2])) continue;
    if (frontendOnlyKey.test(match[2])) continue;
    const prefix = source.slice(0, match.index);
    const catchIndex = prefix.lastIndexOf("catch");
    if (catchIndex < 0) continue;
    const between = prefix.slice(catchIndex, match.index);
    const opens = (between.match(/\{/g) || []).length;
    const closes = (between.match(/\}/g) || []).length;
    if (opens <= closes) continue;
    matches.push({ index: match.index, label: "custom translated failure text in an interface catch" });
  }
  return matches;
}

function sourceFiles(directory) {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const fullPath = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      return entry.name === "generated" ? [] : sourceFiles(fullPath);
    }
    return /\.(?:ts|tsx)$/.test(entry.name) ? [fullPath] : [];
  });
}

const failures = [];
for (const filePath of sourceFiles(sourceDir)) {
  const source = fs.readFileSync(filePath, "utf8");
  for (const { label, pattern } of forbiddenPatterns) {
    for (const match of source.matchAll(pattern)) {
      const line = source.slice(0, match.index).split("\n").length;
      failures.push(`${path.relative(sourceDir, filePath)}:${line} ${label}`);
    }
  }
  for (const { index, label } of collectUserVisibleErrorFieldMatches(source)) {
    const line = source.slice(0, index).split("\n").length;
    failures.push(`${path.relative(sourceDir, filePath)}:${line} ${label}`);
  }
  for (const { index, label } of collectCatchHardcodedMatches(source)) {
    const line = source.slice(0, index).split("\n").length;
    failures.push(`${path.relative(sourceDir, filePath)}:${line} ${label}`);
  }
}

if (failures.length > 0) {
  console.error(failures.join("\n"));
  console.error("Interface errors must be resolved through i18n/errors.");
  process.exit(1);
}

console.log("Interface error prompts do not expose raw backend messages.");
