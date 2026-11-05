// This is the publisher's empty-analysis placeholder, not a substantive qualification.
export const visiblePaperAnalysis = (value: unknown): string => typeof value === 'string'
  && value.trim() !== '该项具体结论尚未获得充分正文支持，需进一步核验。' ? value.trim() : '';
