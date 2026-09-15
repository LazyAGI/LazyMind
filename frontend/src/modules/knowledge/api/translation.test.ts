import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks=vi.hoisted(()=>({get:vi.fn(),post:vi.fn()}));
vi.mock("@/components/request",()=>({BASE_URL:"",axiosInstance:mocks}));

import { translateSelectionText, TranslationUnavailableError } from "./translation";

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
});
