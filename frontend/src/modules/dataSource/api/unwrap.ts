// Unwrap the optional `{ data: T }` envelope returned by some endpoints.
// When the payload is not enveloped it is returned as-is.
export function unwrapApiData<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as { data?: T }).data as T;
  }
  return payload as T;
}
