import { describe, expect, it } from "vitest";
import {
  DEFAULT_DATA_SOURCE_FILE_TYPES,
} from "../constants/options";
import {
  getBindingFileTypes,
  getDataSourceFileTypeExtensions,
  getDataSourceFileTypeIncludePatterns,
  getExtensionsFromIncludePatterns,
  normalizeDataSourceFileTypes,
} from "./fileTypes";

describe("normalizeDataSourceFileTypes", () => {
  it("returns defaults when input is empty or not an array", () => {
    expect(normalizeDataSourceFileTypes(undefined)).toEqual(DEFAULT_DATA_SOURCE_FILE_TYPES);
    expect(normalizeDataSourceFileTypes([] as never)).toEqual(DEFAULT_DATA_SOURCE_FILE_TYPES);
  });

  it("expands legacy aggregate types into concrete extensions", () => {
    const result = normalizeDataSourceFileTypes(["word", "excel"] as never);
    expect(result).toEqual(expect.arrayContaining(["doc", "docx", "xls", "xlsx", "csv"]));
  });

  it("deduplicates and filters out unknown values", () => {
    const result = normalizeDataSourceFileTypes(["pdf", "pdf", "not-a-type"] as never);
    expect(result).toEqual(["pdf"]);
  });
});

describe("getDataSourceFileTypeExtensions / getDataSourceFileTypeIncludePatterns", () => {
  it("maps selected file types to their extensions and glob patterns", () => {
    expect(getDataSourceFileTypeExtensions(["pdf"] as never)).toEqual(["pdf"]);
    expect(getDataSourceFileTypeIncludePatterns(["pdf"] as never)).toEqual(["**/*.pdf"]);
  });
});

describe("getExtensionsFromIncludePatterns", () => {
  it("extracts extensions from glob-style include patterns", () => {
    expect(getExtensionsFromIncludePatterns(["**/*.pdf", "**/*.DOCX"])).toEqual([
      "pdf",
      "docx",
    ]);
  });

  it("ignores non-array input and unmatched patterns", () => {
    expect(getExtensionsFromIncludePatterns("not-an-array")).toEqual([]);
    expect(getExtensionsFromIncludePatterns(["no-extension-here"])).toEqual([]);
  });
});

describe("getBindingFileTypes", () => {
  it("falls back to provided fallback or defaults when there are no extensions", () => {
    expect(getBindingFileTypes(null, ["png"] as never)).toEqual(["png"]);
    expect(getBindingFileTypes(null)).toEqual(DEFAULT_DATA_SOURCE_FILE_TYPES);
  });

  it("resolves file types from binding include_extensions", () => {
    const binding = { include_extensions: ["pdf", "docx"] } as never;
    expect(getBindingFileTypes(binding)).toEqual(expect.arrayContaining(["pdf", "docx"]));
  });

  it("resolves file types from provider_options include_patterns", () => {
    const binding = {
      provider_options: { include_patterns: ["**/*.png"] },
    } as never;
    expect(getBindingFileTypes(binding)).toEqual(["png"]);
  });

  it("falls back when extensions do not map to any known file type", () => {
    const binding = { include_extensions: ["xyz"] } as never;
    expect(getBindingFileTypes(binding, ["png"] as never)).toEqual(["png"]);
  });
});
