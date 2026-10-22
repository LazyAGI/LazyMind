const readCode = (value: unknown): string =>
  value === undefined || value === null ? "" : String(value).trim();

const readSemanticCode = (error: unknown): string => {
  const payload = (error as any)?.response?.data ?? error;
  return readCode(payload?.data?.code ?? payload?.error?.code ?? payload?.code);
};

export const isSkillPackageTooLargeError = (error: unknown): boolean =>
  (error as { response?: { status?: number } } | null)?.response?.status === 413 ||
  readSemanticCode(error) === "payload_too_large";

export const isSkillSourceAmbiguousError = (error: unknown): boolean =>
  ["github_multiple_skills", "skill_md_ambiguous"].includes(readSemanticCode(error));

export const isSkillAlreadyExistsError = (error: unknown): boolean => {
  const payload = (error as any)?.response?.data ?? error;
  const semanticCode = readCode(
    payload?.data?.code ?? payload?.error?.code,
  );
  const errorCode = readCode(payload?.code);

  return semanticCode === "skill_already_exists" ||
    semanticCode === "path_exists" || errorCode === "2001108";
};
