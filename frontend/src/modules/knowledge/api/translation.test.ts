import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks=vi.hoisted(()=>({get:vi.fn(),post:vi.fn()}));
vi.mock("@/components/request",()=>({BASE_URL:"",axiosInstance:mocks}));

import { getTranslationServiceStatus, translateSelectionText, TranslationUnavailableError } from "./translation";

describe("translateSelectionText",()=>{
  beforeEach(()=>vi.resetAllMocks());
  it("uses the built-in dictionary without calling the translation API",async()=>{
    mocks.get.mockResolvedValueOnce({data:{data:{items:[{source_name:"ECDICT",senses:[{translation:"解锁；开启"}]}]}}});
    await expect(translateSelectionText("Unlocks")).resolves.toMatchObject({translated_text:"解锁；开启",source:"dictionary:ECDICT"});
    expect(mocks.post).not.toHaveBeenCalled();
  });
  it("reports a dictionary miss when no translation service is configured",async()=>{
    mocks.get.mockResolvedValueOnce({data:{data:{items:[]}}});
    mocks.post.mockRejectedValueOnce({response: {status: 412}});
    await expect(translateSelectionText("notindictionary")).rejects.toEqual(expect.objectContaining<Partial<TranslationUnavailableError>>({reason:"dictionary_not_found"}));
    expect(mocks.post).toHaveBeenCalledTimes(1);
  });
  it("falls back to the translation API after a dictionary miss",async()=>{
    mocks.get.mockResolvedValueOnce({data:{data:{items:[]}}});
    mocks.post.mockResolvedValueOnce({data:{data:{translated_text:"后备翻译",source:"en",target:"zh"}}});
    await expect(translateSelectionText("fallbackword")).resolves.toMatchObject({translated_text:"后备翻译"});
  });
  it("sends an explicit source and opposite target for a selected translation service",async()=>{
    mocks.post.mockResolvedValueOnce({data:{data:{translated_text:"Hello",source:"zh",target:"en"}}});
    await expect(translateSelectionText("你好", "zh")).resolves.toMatchObject({translated_text:"Hello"});
    expect(mocks.post).toHaveBeenCalledWith("/api/core/translation:translate",{text:"你好",source:"zh",target:"en"});
  });
  it("uses the selected translation service rather than the dictionary when the source is explicit",async()=>{
    mocks.post.mockResolvedValueOnce({data:{data:{translated_text:"你好",source:"en",target:"zh"}}});
    await expect(translateSelectionText("Hello", "en")).resolves.toMatchObject({translated_text:"你好"});
    expect(mocks.get).not.toHaveBeenCalled();
    expect(mocks.post).toHaveBeenCalledWith("/api/core/translation:translate",{text:"Hello",source:"en",target:"zh"});
  });
});

it("reads the selected provider and document capability without exposing credentials",async()=>{
  mocks.get.mockResolvedValueOnce({data:{data:{configured:true,provider:"Google Translate Web",selected_group_id:"google-group",document_supported:true,document_providers:[
    {group_id:"google-group",provider_name:"Google Translate Web",group_name:"Free web"},
    {group_id:"baidu-group",provider_name:"Baidu Translation",group_name:"Baidu account"},
  ]}}});
  await expect(getTranslationServiceStatus()).resolves.toEqual({configured:true,provider:"Google Translate Web",selected_group_id:"google-group",document_supported:true,document_providers:[
    {group_id:"google-group",provider_name:"Google Translate Web",group_name:"Free web"},
    {group_id:"baidu-group",provider_name:"Baidu Translation",group_name:"Baidu account"},
  ]});
});
