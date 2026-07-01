export function getDataSourceErrorMessage(error: unknown) {
  const payload = (error as any)?.response?.data ?? error;
  const detail = payload?.detail;

  if (Array.isArray(detail)) {
    const messages = detail
      .map((item) => (typeof item === "string" ? item : item?.message || item?.msg))
      .filter(Boolean);

    if (messages.length > 0) {
      return messages.join("；");
    }
  }

  return `${payload?.message || (error as any)?.message || ""}`.trim();
}

export function isKnowledgeBaseNameDuplicatedError(error: unknown) {
  const payload = (error as any)?.response?.data ?? error;
  const errorCode = `${payload?.code || payload?.error_code || payload?.errorCode || ""}`.trim();
  const rawMessage = getDataSourceErrorMessage(error).toLowerCase();

  return errorCode === "2001102" || rawMessage === "dataset name already exists";
}
