// Keep this object stable: React-PDF reloads documents when options change.
export const pdfDocumentOptions = {
  cMapUrl: `${import.meta.env.BASE_URL}pdfjs/cmaps/`,
  cMapPacked: true,
};
