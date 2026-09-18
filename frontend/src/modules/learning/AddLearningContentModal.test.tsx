import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, vi } from "vitest";
import AddLearningContentModal from "./AddLearningContentModal";
import * as api from "./api";

vi.mock("./api",()=>({getLearningCatalog:vi.fn(),listLearningBooks:vi.fn(),resolveLearningContent:vi.fn(),createLearningBook:vi.fn()}));
const capability={key:"classical_definition",version:1,name_i18n_key:"文言解释",description_i18n_key:"",local_only:true,languages:["lzh"],subject_kinds:["word"],provider_pipeline:[],allowed_question_types:["text_input"],default_question_types:["text_input"],fields:[{key:"meaning_in_context",type:"text",label_i18n_key:"语境义",help_i18n_key:"",required:true,editable:true}]};

beforeEach(()=>vi.clearAllMocks());

it("keeps adding to a collection available for translation",async()=>{
 const translationCapability={...capability,key:"classical_translation",name_i18n_key:"文言翻译",subject_kinds:["sentence"],fields:[{key:"translated_text",type:"text",label_i18n_key:"译文",help_i18n_key:"",required:true,editable:true}]};
 vi.mocked(api.getLearningCatalog).mockResolvedValue({capabilities:[translationCapability],question_types:[],profiles:[],local_available:true});
 vi.mocked(api.listLearningBooks).mockResolvedValue([{id:"good",name:"古文翻译",description:"",capability_key:"classical_translation",question_types_json:"[]"},{id:"bad",name:"英语",description:"",capability_key:"english_definition",question_types_json:"[]"}]);
 vi.mocked(api.resolveLearningContent).mockResolvedValue({content:{id:""},value:{translated_text:"两只兔子贴着地面跑。"},source:"llm"});
 render(<AddLearningContentModal value={{capabilityKey:"classical_translation",selection:{text:"双兔傍地走",page:1,context:"双兔傍地走"}}} datasetId="ds" documentId="doc" onClose={()=>{}} onAdded={()=>{}}/>);
 expect(await screen.findByText("两只兔子贴着地面跑。")).toBeInTheDocument();
 expect(screen.queryByRole("combobox",{name:"学习集"})).not.toBeInTheDocument();
 expect(api.resolveLearningContent).toHaveBeenCalledWith(expect.objectContaining({preview:true}));
 fireEvent.click(screen.getByRole("button",{name:"加入学习集"}));
 expect(screen.getByDisplayValue("两只兔子贴着地面跑。")).toBeInTheDocument();
 fireEvent.mouseDown(screen.getByRole("combobox",{name:"学习集"}));expect((await screen.findAllByText("古文翻译")).length).toBeGreaterThan(0);expect(screen.queryByText("英语")).not.toBeInTheDocument();fireEvent.click(screen.getByRole("button",{name:"确认加入"}));await waitFor(()=>expect(api.resolveLearningContent).toHaveBeenLastCalledWith(expect.objectContaining({value:expect.objectContaining({translated_text:"两只兔子贴着地面跑。"}),book_ids:["good"]})));
});

it("shows non-translation results without offering a learning collection",async()=>{
 const chineseCapability={...capability,key:"chinese_definition",name_i18n_key:"汉语解释",languages:["zh-Hans"],fields:[{key:"definition",type:"text",label_i18n_key:"释义",help_i18n_key:"",required:true,editable:true}]};
 vi.mocked(api.getLearningCatalog).mockResolvedValue({capabilities:[chineseCapability],question_types:[],profiles:[],local_available:true});
 vi.mocked(api.listLearningBooks).mockResolvedValue([]);
 vi.mocked(api.resolveLearningContent).mockResolvedValue({content:{id:"content-zh"},value:{definition:"行走；行动。"},source:"chinese_dictionary"});
 render(<AddLearningContentModal value={{capabilityKey:"chinese_definition",selection:{text:"行",page:1,context:"行万里路"}}} datasetId="ds" documentId="doc" onClose={()=>{}} onAdded={()=>{}}/>);
 expect(await screen.findByText("行走；行动。")).toBeInTheDocument();
 expect(screen.getByText(/chinese_dictionary/)).toBeInTheDocument();
 expect(screen.queryByText("学习集")).not.toBeInTheDocument();
 expect(screen.queryByRole("button",{name:"加入学习集"})).not.toBeInTheDocument();
 expect(api.listLearningBooks).not.toHaveBeenCalled();
 expect(api.createLearningBook).not.toHaveBeenCalled();
});

it("clears pinyin when the next explanation does not return it",async()=>{
 const explanationCapability={...capability,key:"chinese_definition",name_i18n_key:"汉语解释",languages:["zh-Hans"],fields:[{key:"pinyin",type:"string",label_i18n_key:"拼音",help_i18n_key:"",required:false,editable:true},...capability.fields]};
 vi.mocked(api.getLearningCatalog).mockResolvedValue({capabilities:[explanationCapability],question_types:[],profiles:[],local_available:true});
 vi.mocked(api.listLearningBooks).mockResolvedValue([{id:"book",name:"汉语",description:"",capability_key:"chinese_definition",question_types_json:"[]"}]);
 vi.mocked(api.resolveLearningContent)
  .mockResolvedValueOnce({content:{id:"first"},value:{pinyin:"gāo pín",meaning_in_context:"出现频率较高"},source:"llm"})
  .mockResolvedValueOnce({content:{id:"second"},value:{meaning_in_context:"车辆改变行驶车道"},source:"llm"});
 const view=render(<AddLearningContentModal value={{capabilityKey:"chinese_definition",selection:{text:"高频",page:1}}} datasetId="ds" documentId="doc" onClose={()=>{}} onAdded={()=>{}}/>);
 expect(await screen.findByText("gāo pín")).toBeInTheDocument();
 view.rerender(<AddLearningContentModal value={{capabilityKey:"chinese_definition",selection:{text:"变道",page:1}}} datasetId="ds" documentId="doc" onClose={()=>{}} onAdded={()=>{}}/>);
 expect(await screen.findByText("车辆改变行驶车道")).toBeInTheDocument();
 expect(screen.queryByText("gāo pín")).not.toBeInTheDocument();
 expect(screen.queryByRole("button",{name:"加入学习集"})).not.toBeInTheDocument();
});
