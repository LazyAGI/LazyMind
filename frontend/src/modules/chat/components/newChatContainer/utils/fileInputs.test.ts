import { describe, expect, it } from "vitest";
import { getFileUrls } from "./fileInputs";

describe("getFileUrls", () => {
  it("returns an empty array when files is undefined", () => {
    expect(getFileUrls(undefined)).toEqual([]);
  });

  it("maps files to uri/base64 pairs, matching images by uid", () => {
    const files = [
      { uid: "1", uri: "file://a" },
      { uid: "2", uri: "file://b" },
    ] as never;
    const images = [
      { uid: "1", base64: "data:base64,aaa" },
      { uid: "3", base64: "data:base64,ccc" },
    ] as never;

    const result = getFileUrls(files, images);

    expect(result).toEqual([
      { uri: "file://a", base64: "data:base64,aaa" },
      { uri: "file://b", base64: undefined },
    ]);
  });

  it("returns empty base64 strings when no images are provided", () => {
    const files = [{ uid: "1", uri: "file://a" }] as never;

    const result = getFileUrls(files);

    expect(result).toEqual([{ uri: "file://a", base64: "" }]);
  });
});
