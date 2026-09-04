import { describe, expect, it, vi } from "vitest";
import { isPreferenceOrganizing } from "./currentMemoryApi";

vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "" }));

describe("isPreferenceOrganizing", () => {
  it("recognizes the reassigned maintenance code", () => {
    expect(isPreferenceOrganizing({ response: { data: { code: 2002361 } } })).toBe(true);
  });

  it("recognizes the semantic error code", () => {
    expect(isPreferenceOrganizing({
      response: { data: { data: { error_code: "preference_organizing" } } },
    })).toBe(true);
  });

  it.each([2002321, 2002322, 2002323, 2002324, 2002325, 2002326, 2002365, 2002366])(
    "does not treat unrelated error %i as an Organizer write freeze",
    (code) => {
      expect(isPreferenceOrganizing({ response: { data: { code } } })).toBe(false);
    },
  );

  it.each([undefined, null, {}, new Error("network error")])(
    "handles missing response data: %s",
    (error) => {
      expect(isPreferenceOrganizing(error)).toBe(false);
    },
  );
});
