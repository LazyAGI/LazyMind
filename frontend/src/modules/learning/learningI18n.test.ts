import { describe, expect, it } from "vitest";
import zh from "@/i18n/locales/zh-CN";
import en from "@/i18n/locales/en-US";

const get=(root:unknown,path:string)=>path.split(".").reduce<unknown>((value,key)=>value&&typeof value==="object"?(value as Record<string,unknown>)[key]:undefined,root);
const keys=[
 "learning.capability.englishDefinition.name","learning.capability.chineseDefinition.name","learning.capability.classicalDefinition.name","learning.capability.generalTranslation.name","learning.capability.classicalTranslation.name","learning.capability.literaryAppreciation.name","learning.capability.pinyin.name",
 "learning.questionType.singleChoice.name","learning.questionType.textInput.name","learning.questionType.cloze.name","learning.questionType.trueFalse.name","learning.questionType.translationResponse.name","learning.questionType.shortAnswer.name","learning.questionType.rubricSelfAssessment.name",
 "learning.field.phonetic","learning.field.pinyin","learning.field.meaning","learning.field.meaningInContext","learning.field.examples","learning.field.phenomena","learning.field.citations","learning.field.translation","learning.field.targetLanguage","learning.field.keyWords","learning.field.specialPatterns","learning.field.techniques","learning.field.evidence","learning.field.effects",
];
describe("learning i18n contract",()=>{it.each(keys)("contains %s in both locales",key=>{expect(get(zh,key)).toBeTruthy();expect(get(en,key)).toBeTruthy()})});

const leafKeys=(value:unknown,prefix=""):string[]=>value&&typeof value==="object"?Object.entries(value).flatMap(([key,item])=>leafKeys(item,prefix?`${prefix}.${key}`:key)):[prefix];
describe("learning locale parity",()=>{it("keeps every learning key in both locales",()=>{expect(leafKeys(zh.learning).sort()).toEqual(leafKeys(en.learning).sort())})});
