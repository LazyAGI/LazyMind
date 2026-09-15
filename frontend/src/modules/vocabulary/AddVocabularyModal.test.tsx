import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import AddVocabularyModal from "./AddVocabularyModal";
import { getTranslationStatus, translateText } from "@/modules/knowledge/api/translation";
import { resolveVocabularySelection } from "./api";

vi.mock("@/modules/knowledge/api/translation", () => ({ getTranslationStatus: vi.fn(async()=>false), translateText: vi.fn(), isSingleEnglishWord:(text:string)=>/^[A-Za-z]+$/.test(text) }));
vi.mock("./api", () => ({
  resolveVocabularySelection: vi.fn(async () => ({
    term: "workloads",
    sentence: "Diverse workloads require flexible compute.",
    provider: "local",
    existing: null,
    dictionary: null,
    wordbooks: null,
  })),
  addVocabularyWord: vi.fn(),
  lookupDictionary: vi.fn(async () => [{ id:"e1", term:"specification", phonetic:"/ˌspesɪfɪˈkeɪʃn/", source_name:"ECDICT", source_version:"1", license_id:"MIT", source_locator:"ECDICT", senses:[{id:"s1",part_of_speech:"n.",definition:"a detailed description",translation:"规格；说明"}], examples:[] }]),
}));

describe("AddVocabularyModal", () => {
  it("handles a selection when dictionary and wordbooks are absent", async () => {
    render(<AddVocabularyModal
      selection={{ text: "workloads", context: "Diverse workloads require flexible compute.", page: 1 }}
      datasetId="dataset-1"
      documentId="document-1"
      onClose={vi.fn()}
      onAdded={vi.fn()}
    />);
    expect(await screen.findByDisplayValue("workloads")).toBeTruthy();
    expect(screen.getByText("词典未找到可靠的中文释义，请换一个目标词后重试")).toBeTruthy();
    expect(screen.queryByText("Provider")).toBeNull();
    expect(screen.getByText("加入单词本")).toBeTruthy();
  });
  it("looks up a changed term after the input loses focus", async () => {
    render(<AddVocabularyModal selection={{text:"workloads",context:"Diverse workloads require flexible compute.",page:1}} datasetId="dataset-1" documentId="document-1" onClose={vi.fn()} onAdded={vi.fn()}/>);
    const input=await screen.findByDisplayValue("workloads");
    fireEvent.change(input,{target:{value:"specification"}});fireEvent.blur(input);
    await waitFor(()=>expect(screen.getByText("规格；说明")).toBeTruthy());
  });
  it("shows one selected dictionary and translates the initial sentence when configured", async()=>{
    vi.mocked(getTranslationStatus).mockResolvedValueOnce(true);vi.mocked(translateText).mockResolvedValueOnce({translated_text:"完整技术栈",source:"en",target:"zh"});
    vi.mocked(resolveVocabularySelection).mockResolvedValueOnce({term:"stack",sentence:"Full stack",provider:"local",dictionary:[{id:"ec",term:"stack",phonetic:"/stæk/",source_name:"ECDICT",source_version:"1",license_id:"MIT",source_locator:"x",senses:[{id:"s1",part_of_speech:"n.",definition:"pile\\nlayer",translation:"n. 堆叠\\nvt. 堆放"}],examples:[]},{id:"fd",term:"stack",phonetic:"/stæk/",source_name:"FreeDict eng-zho",source_version:"1",license_id:"CC",source_locator:"x",senses:[{id:"s2",part_of_speech:"n",definition:"pile",translation:"堆；电池"}],examples:[]}],wordbooks:[{id:"default",name:"默认生词本",description:""}]});
    render(<AddVocabularyModal selection={{text:"stack",context:"Full stack",page:1}} datasetId="dataset-1" documentId="document-1" onClose={vi.fn()} onAdded={vi.fn()}/>);
    expect(await screen.findByText("堆叠")).toBeTruthy();expect(screen.getByText("堆放")).toBeTruthy();expect(screen.queryByText("堆；电池")).toBeNull();
    await waitFor(()=>expect(screen.getByDisplayValue("完整技术栈")).toBeTruthy());expect(screen.queryByRole("button",{name:"翻译原句"})).toBeNull();
    const term=screen.getByDisplayValue("stack");fireEvent.change(term,{target:{value:"specification"}});fireEvent.keyDown(term,{key:"Enter",code:"Enter",charCode:13});
    expect(await screen.findByText("当前内容")).toBeTruthy();expect(screen.getByText("新词典内容")).toBeTruthy();
  });
});
