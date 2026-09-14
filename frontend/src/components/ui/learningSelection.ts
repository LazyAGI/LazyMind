export interface LearningSelectionAction {
  key: string;
  label: string;
  disabled?: boolean;
  disabledTip?: string;
  languages?: string[];
  subjectKinds?: string[];
}

export function isLearningActionCompatible(action: LearningSelectionAction, text: string): boolean {
  const value = text.trim();
  if (!value) return false;
  const chars = Array.from(value);
  const hasHan = chars.some((char) => /\p{Script=Han}/u.test(char));
  const hasLatin = chars.some((char) => /\p{Script=Latin}/u.test(char));
  const language = hasHan ? "zh-Hans" : hasLatin ? "en" : "und";
  let subjectKind = "phrase";
  if (hasHan) subjectKind = chars.length === 1 ? "character" : chars.length > 20 ? "passage" : /[。！？；，]/u.test(value) ? "sentence" : "word";
  else if (hasLatin) subjectKind = /\s/u.test(value) ? "phrase" : "word";
  const languageMatches = !action.languages?.length || action.languages.includes("*") || action.languages.includes(language) || (hasHan && action.languages.some((item) => item === "zh-Hant" || item === "lzh"));
  return languageMatches && (!action.subjectKinds?.length || action.subjectKinds.includes(subjectKind));
}
