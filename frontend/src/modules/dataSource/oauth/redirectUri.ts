function isLoopbackHostname(hostname: string) {
  const normalized = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  return normalized === "localhost" || normalized === "127.0.0.1" || normalized === "::1";
}

function isRawIpHostname(hostname: string) {
  const normalized = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  return /^(?:\d{1,3}\.){3}\d{1,3}$/.test(normalized) || normalized.includes(":");
}

function hasPublicDomainShape(hostname: string) {
  const normalized = hostname.toLowerCase().replace(/\.$/, "");
  return (
    normalized.includes(".") &&
    !normalized.endsWith(".local") &&
    !normalized.endsWith(".localhost") &&
    !normalized.endsWith(".internal") &&
    !normalized.endsWith(".lan")
  );
}

export function isGoogleOAuthRedirectUriSupported(value: string) {
  try {
    const url = new URL(value);
    if (isLoopbackHostname(url.hostname)) {
      return url.protocol === "http:" || url.protocol === "https:";
    }
    return (
      url.protocol === "https:" &&
      !isRawIpHostname(url.hostname) &&
      hasPublicDomainShape(url.hostname)
    );
  } catch {
    return false;
  }
}
