import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  CLOUD_DOCUMENTS_FEISHU_PATH,
  CLOUD_DOCUMENTS_GOOGLE_DRIVE_PATH,
  CLOUD_DOCUMENTS_LOCAL_PATH,
  CLOUD_DOCUMENTS_PATH,
  getCloudDocumentsUrl,
} from "./cloudDocumentUrls";

describe("getCloudDocumentsUrl", () => {
  const originalBasename = (window as Window & { BASENAME?: string }).BASENAME;

  afterEach(() => {
    (window as Window & { BASENAME?: string }).BASENAME = originalBasename;
  });

  it("builds the base cloud documents url when no provider is given", () => {
    (window as Window & { BASENAME?: string }).BASENAME = "";
    expect(getCloudDocumentsUrl()).toBe(`${window.location.origin}/cloud-documents`);
  });

  it("appends the provider-specific path for feishu/local/googledrive", () => {
    (window as Window & { BASENAME?: string }).BASENAME = "";
    expect(getCloudDocumentsUrl("feishu")).toBe(`${window.location.origin}/cloud-documents/feishu`);
    expect(getCloudDocumentsUrl("local")).toBe(`${window.location.origin}/cloud-documents/local`);
    expect(getCloudDocumentsUrl("googledrive")).toBe(
      `${window.location.origin}/cloud-documents/google-drive`,
    );
  });

  it("strips a trailing slash from BASENAME before composing the url", () => {
    (window as Window & { BASENAME?: string }).BASENAME = "/app/";
    expect(getCloudDocumentsUrl("feishu")).toBe(
      `${window.location.origin}/app/cloud-documents/feishu`,
    );
  });
});

describe("cloud documents path constants", () => {
  it("exposes stable route path constants", () => {
    expect(CLOUD_DOCUMENTS_PATH).toBe("/cloud-documents");
    expect(CLOUD_DOCUMENTS_LOCAL_PATH).toBe("/cloud-documents/local");
    expect(CLOUD_DOCUMENTS_FEISHU_PATH).toBe("/cloud-documents/feishu");
    expect(CLOUD_DOCUMENTS_GOOGLE_DRIVE_PATH).toBe("/cloud-documents/google-drive");
  });
});
