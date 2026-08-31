import {
  downloadDesktopFile,
  getDesktopPlatform,
  hasDesktopFileBridge,
  saveFileAs,
  showItemInFolder,
  type DesktopArtifactFilePayload,
} from "@/runtime/desktopBridge";
import { appendDownloadParam } from "./artifactLinks";

export function isAppleDesktopPlatform(): boolean {
  const platform = getDesktopPlatform();
  if (platform) {
    return platform === "darwin";
  }
  if (typeof navigator === "undefined") {
    return false;
  }
  return /Mac|iPhone|iPad/i.test(navigator.platform)
    || /Macintosh/i.test(navigator.userAgent);
}

async function readArtifactFilePayload(
  source: string,
  filename: string,
): Promise<DesktopArtifactFilePayload> {
  const payload: DesktopArtifactFilePayload = { source, filename };
  if (source.startsWith("blob:")) {
    const response = await fetch(source);
    if (!response.ok) {
      throw new Error(`Failed to read file (${response.status})`);
    }
    payload.data = await response.arrayBuffer();
  }
  return payload;
}

function triggerBrowserDownload(source: string, filename: string) {
  const anchor = document.createElement("a");
  anchor.href = source.startsWith("blob:")
    ? source
    : appendDownloadParam(source);
  anchor.download = filename || "download";
  anchor.rel = "noreferrer";
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
}

async function saveInBrowser(
  source: string,
  filename: string,
): Promise<"saved" | "canceled"> {
  const picker = (
    window as Window & {
      showSaveFilePicker?: (options: {
        suggestedName?: string;
      }) => Promise<{
        createWritable: () => Promise<{
          write: (data: Blob) => Promise<void>;
          close: () => Promise<void>;
        }>;
      }>;
    }
  ).showSaveFilePicker;
  if (typeof picker !== "function") {
    triggerBrowserDownload(source, filename);
    return "saved";
  }
  try {
    const handle = await picker({ suggestedName: filename || "download" });
    const writable = await handle.createWritable();
    const response = await fetch(source);
    if (!response.ok) {
      throw new Error(`Failed to download file (${response.status})`);
    }
    await writable.write(await response.blob());
    await writable.close();
    return "saved";
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      return "canceled";
    }
    throw error;
  }
}

export async function revealArtifactFile(
  source: string,
  filename: string,
): Promise<void> {
  if (!source) {
    throw new Error("File is not ready");
  }
  const result = await showItemInFolder(
    await readArtifactFilePayload(source, filename),
  );
  if (!result.ok) {
    throw result.error instanceof Error
      ? result.error
      : new Error("Unable to reveal file");
  }
}

export async function saveArtifactFileAs(
  source: string,
  filename: string,
): Promise<"saved" | "canceled"> {
  if (!source) {
    throw new Error("File is not ready");
  }
  if (!hasDesktopFileBridge()) {
    return saveInBrowser(source, filename);
  }
  const result = await saveFileAs(
    await readArtifactFilePayload(source, filename),
  );
  if (result.canceled) {
    return "canceled";
  }
  if (!result.ok) {
    throw result.error instanceof Error
      ? result.error
      : new Error("Unable to save file");
  }
  return "saved";
}

export async function downloadArtifactFile(
  source: string,
  filename: string,
): Promise<void> {
  if (!source) {
    throw new Error("File is not ready");
  }
  if (!hasDesktopFileBridge()) {
    triggerBrowserDownload(source, filename);
    return;
  }
  const result = await downloadDesktopFile(
    await readArtifactFilePayload(source, filename),
  );
  if (!result.ok) {
    throw result.error instanceof Error
      ? result.error
      : new Error("Unable to download file");
  }
}
