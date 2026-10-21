class FileUtils {
  /**
   * Format file size 1024 => 1.0KB.
   *
   * @param fileSize - File Size(B).
   * @param digits - Decimal places.
   */
  public static formatFileSize = (fileSize?: number | string, digits = 1) => {
    if (!Number(fileSize)) {
      return "0B";
    }

    const units = ["B", "KB", "MB", "GB", "TB", "PB", "EB", "ZB"];
    let unitsIndex = 0;
    let size = Number(fileSize);
    while (size >= 1024 && unitsIndex < units.length - 1) {
      size = size / 1024;
      unitsIndex = unitsIndex + 1;
    }

    return size.toFixed(digits) + units[unitsIndex];
  };

  // Upload file to url.
  public static putFile = async (params: {
    file: File | Blob;
    url: string;
  }) => {
    const { file, url } = params;
    if (!url || !file) {
      return Promise.reject({});
    }

    return fetch(url, {
      method: "put",
      body: file,
      signal: this.timeoutSignal(5 * 60 * 1000),
    }).then((response) => {
      return response.ok ? response : Promise.reject({});
    });
  };

  // Get file name suffix.
  public static getSuffix = (fileName: string, withDot?: boolean) => {
    const index = fileName?.lastIndexOf(".");
    if (index >= 0) {
      const suffix = withDot
        ? fileName.slice(index)
        : fileName.slice(index + 1);
      return suffix.toLocaleLowerCase();
    }
    return "";
  };

  public static getFileTypeFromURI = (uri: string) => {
    if (!uri) {
      return "";
    }
    try {
      const url = new URL(uri, window.location.origin);
      // Object/data URLs have no filename; the viewer must use its supplied name.
      if (url.protocol === "blob:" || url.protocol === "data:") return "";
      const pathname = url.pathname;
      return this.getSuffix(pathname);
    } catch {
      return this.getSuffix(uri.split("?")[0] || uri);
    }
  };

  // Normalize file extension to lowercase in a path
  public static normalizeExtensionToLower = (fileName: string): string => {
    const index = fileName?.lastIndexOf(".");
    if (index >= 0) {
      const base = fileName.slice(0, index);
      const ext = fileName.slice(index).toLocaleLowerCase();
      return base + ext;
    }
    return fileName;
  };

  public static timeoutSignal(ms: number): AbortSignal {
    const controller = new AbortController();
    setTimeout(() => controller.abort(), ms);
    return controller.signal;
  }
}

export default FileUtils;
