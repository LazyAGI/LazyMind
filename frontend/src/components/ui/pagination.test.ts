import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { getLocalizedTablePagination } from "./pagination";

const t = ((key: string) => key) as unknown as TFunction;

describe("getLocalizedTablePagination", () => {
  it("returns falsy pagination unchanged", () => {
    expect(getLocalizedTablePagination(false, t)).toBe(false);
    expect(getLocalizedTablePagination(undefined, t)).toBe(undefined);
  });

  it("merges localized locale strings into pagination config", () => {
    const result = getLocalizedTablePagination({ current: 1, pageSize: 10 }, t);
    expect(result).toMatchObject({
      current: 1,
      pageSize: 10,
      locale: {
        items_per_page: "common.itemsPerPageSuffix",
        page_size: "common.pageSize",
      },
    });
  });

  it("preserves existing locale fields while adding new ones", () => {
    const result = getLocalizedTablePagination(
      { locale: { jump_to: "Jump" } },
      t,
    );
    expect(result).toMatchObject({
      locale: {
        jump_to: "Jump",
        items_per_page: "common.itemsPerPageSuffix",
        page_size: "common.pageSize",
      },
    });
  });
});
