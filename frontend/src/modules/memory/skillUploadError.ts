const readCode = (value: unknown): string =>
  value === undefined || value === null ? "" : String(value).trim();

export const isSkillAlreadyExistsError = (error: unknown): boolean => {
  const payload = (error as any)?.response?.data ?? error;
  const semanticCode = readCode(
    payload?.data?.code ?? payload?.error?.code,
  );
  const errorCode = readCode(payload?.code);

  return semanticCode === "path_exists" || errorCode === "2001108";
};
