import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks=vi.hoisted(()=>({get:vi.fn(),post:vi.fn()}));
vi.mock("@/components/request",()=>({BASE_URL:"",axiosInstance:mocks}));

import { getTranslationServiceStatus, translateSelectionText, TranslationUnavailableError } from "./translation";

describe("translateSelectionText",()=>{
  beforeEach(()=>vi.clearAllMocks());
  it("uses the built-in dictionary without calling the translation API",async()=>{
    mocks.get.mockResolvedValueOnce({data:{data:{items:[{source_name:"ECDICT",senses:[{translation:"解锁；开启"}]}]}}});
    await expect(translateSelectionText("Unlocks")).resolves.toMatchObject({translated_text:"解锁；开启",source:"dictionary:ECDICT"});
    expect(mocks.post).not.toHaveBeenCalled();
  });
  it("reports a dictionary miss when no translation service is configured",async()=>{
    mocks.get.mockResolvedValueOnce({data:{data:{items:[]}}}).mockResolvedValueOnce({data:{data:{configured:false}}});
    await expect(translateSelectionText("notindictionary")).rejects.toEqual(expect.objectContaining<Partial<TranslationUnavailableError>>({reason:"dictionary_not_found"}));
    expect(mocks.post).not.toHaveBeenCalled();
  });
  it("falls back to the translation API after a dictionary miss",async()=>{
    mocks.get.mockResolvedValueOnce({data:{data:{items:[]}}}).mockResolvedValueOnce({data:{data:{configured:true}}});
    mocks.post.mockResolvedValueOnce({data:{data:{translated_text:"后备翻译",source:"en",target:"zh"}}});
    await expect(translateSelectionText("fallbackword")).resolves.toMatchObject({translated_text:"后备翻译"});
  });
  it("sends an explicit source and opposite target for MyMemory selection",async()=>{
    mocks.get.mockResolvedValueOnce({data:{data:{configured:true,provider:"MyMemory",document_supported:false}}});
    mocks.post.mockResolvedValueOnce({data:{data:{translated_text:"Hello",source:"zh",target:"en"}}});
    await expect(translateSelectionText("你好", "zh")).resolves.toMatchObject({translated_text:"Hello"});
    expect(mocks.post).toHaveBeenCalledWith("/api/core/translation:translate",{text:"你好",source:"zh",target:"en"});
  });
  it("uses MyMemory rather than the dictionary when the source is explicit",async()=>{
    mocks.get.mockResolvedValueOnce({data:{data:{configured:true,provider:"MyMemory",document_supported:false}}});
    mocks.post.mockResolvedValueOnce({data:{data:{translated_text:"你好",source:"en",target:"zh"}}});
    await expect(translateSelectionText("Hello", "en")).resolves.toMatchObject({translated_text:"你好"});
    expect(mocks.get).toHaveBeenCalledTimes(1);
    expect(mocks.post).toHaveBeenCalledWith("/api/core/translation:translate",{text:"Hello",source:"en",target:"zh"});
  });
});

it("reads the selected provider and document capability without exposing credentials",async()=>{
  mocks.get.mockResolvedValueOnce({data:{data:{configured:true,provider:"Google Translate Web",document_supported:true}}});
  await expect(getTranslationServiceStatus()).resolves.toEqual({configured:true,provider:"Google Translate Web",document_supported:true});
});
