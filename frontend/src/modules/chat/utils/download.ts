export const downloadStream = (data: Blob, fileName: string) => {
  const url = window.URL.createObjectURL(data);
  const link = document.createElement("a");
  link.style.display = "none";
  link.href = url;
  link.setAttribute("download", fileName);
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  window.setTimeout(() => window.URL.revokeObjectURL(url), 0);
};

export const downloadUrl = (url: string, target?: string) => {
  const a = document.createElement("a");
  a.target = target || "_self";
  a.href = url.toString();
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
};

export const downloadFile = (url: string) => {
  const iframe = document.createElement("iframe");
  iframe.style.display = "none";
  iframe.src = url;
  iframe.className = "downloadIframe";
  document.body.appendChild(iframe);
  // setTimeout(() => iframe.remove(), 30 * 1000)
};
