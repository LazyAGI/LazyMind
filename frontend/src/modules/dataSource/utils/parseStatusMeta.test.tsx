import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { getParseStatusMeta } from "./parseStatusMeta";

const t = ((key: string) => key) as TFunction;

describe("getParseStatusMeta", () => {
  it("returns success styling for the parsed status", () => {
    const meta = getParseStatusMeta("parsed", t);
    expect(meta.color).toBe("#12b76a");
    expect(meta.text).toBe("admin.dataSourceParseParsed");
  });

  it("returns a spinning icon for in-progress statuses", () => {
    const reindexing = getParseStatusMeta("reindexing", t);
    expect(reindexing.text).toBe("admin.dataSourceParseReindexing");
    const downloading = getParseStatusMeta("downloading", t);
    expect(downloading.text).toBe("admin.dataSourceParseDownloading");
  });

  it("returns error styling for failure statuses", () => {
    const parseFailed = getParseStatusMeta("parse_failed", t);
    expect(parseFailed.color).toBe("#f04438");
    expect(parseFailed.text).toBe("admin.dataSourceParseParseFailed");

    const downloadFailed = getParseStatusMeta("download_failed", t);
    expect(downloadFailed.color).toBe("#f04438");
    expect(downloadFailed.text).toBe("admin.dataSourceParseDownloadFailed");
  });

  it("falls back to a generic failure meta for an unrecognized status", () => {
    const meta = getParseStatusMeta("unknown_status" as never, t);
    expect(meta.color).toBe("#f04438");
    expect(meta.text).toBe("admin.dataSourceParseFailed");
  });

  it("returns distinct metadata for pending, duplicate, deleted and canceled statuses", () => {
    expect(getParseStatusMeta("pending", t).text).toBe("admin.dataSourceParsePending");
    expect(getParseStatusMeta("duplicate", t).text).toBe("admin.dataSourceParseDuplicate");
    expect(getParseStatusMeta("deleted", t).text).toBe("admin.dataSourceParseDeleted");
    expect(getParseStatusMeta("canceled", t).text).toBe("admin.dataSourceParseCanceled");
  });
});
