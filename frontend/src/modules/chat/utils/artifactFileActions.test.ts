import { afterEach, describe, expect, it, vi } from "vitest";
import {
  downloadDesktopFile,
  hasDesktopFileBridge,
  saveFileAs,
  showItemInFolder,
} from "@/runtime/desktopBridge";
import {
  downloadArtifactFile,
  revealArtifactFile,
  saveArtifactFileAs,
} from "./artifactFileActions";

vi.mock("@/runtime/desktopBridge", () => ({
  showItemInFolder: vi.fn(),
  saveFileAs: vi.fn(),
  downloadDesktopFile: vi.fn(),
  getDesktopPlatform: () => "darwin",
  hasDesktopFileBridge: vi.fn(),
}));

const mockedShowItemInFolder = vi.mocked(showItemInFolder);
const mockedSaveFileAs = vi.mocked(saveFileAs);
const mockedDownloadDesktopFile = vi.mocked(downloadDesktopFile);
const mockedHasDesktopFileBridge = vi.mocked(hasDesktopFileBridge);

describe("artifactFileActions", () => {
  afterEach(() => {
    vi.clearAllMocks();
    document.body.replaceChildren();
  });

  it("reveals a desktop file through the native bridge", async () => {
    mockedHasDesktopFileBridge.mockReturnValue(true);
    mockedShowItemInFolder.mockResolvedValue({ ok: true, path: "/tmp/a.txt" });

    await revealArtifactFile(
      "https://host/api/core/static-files/a.txt",
      "a.txt",
    );

    expect(mockedShowItemInFolder).toHaveBeenCalledWith({
      source: "https://host/api/core/static-files/a.txt",
      filename: "a.txt",
    });
  });

  it("saves and downloads through the desktop bridge when it is injected", async () => {
    mockedHasDesktopFileBridge.mockReturnValue(true);
    mockedSaveFileAs.mockResolvedValue({ ok: true, path: "/tmp/out.txt" });
    mockedDownloadDesktopFile.mockResolvedValue({
      ok: true,
      path: "/tmp/dl.txt",
    });

    await expect(
      saveArtifactFileAs("https://host/file.txt", "out.txt"),
    ).resolves.toBe("saved");
    await downloadArtifactFile("https://host/file.txt", "out.txt");

    expect(mockedSaveFileAs).toHaveBeenCalled();
    expect(mockedDownloadDesktopFile).toHaveBeenCalled();
  });

  it("falls back to a browser download outside desktop", async () => {
    mockedHasDesktopFileBridge.mockReturnValue(false);
    const click = vi.fn();
    const originalCreate = document.createElement.bind(document);
    vi.spyOn(document, "createElement").mockImplementation((tagName: string) => {
      const el = originalCreate(tagName);
      if (tagName === "a") {
        Object.defineProperty(el, "click", { value: click });
      }
      return el;
    });

    await downloadArtifactFile("https://host/file.txt", "out.txt");

    expect(mockedDownloadDesktopFile).not.toHaveBeenCalled();
    expect(click).toHaveBeenCalled();
  });
});
