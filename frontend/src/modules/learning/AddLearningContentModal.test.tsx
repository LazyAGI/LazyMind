import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";
import AddLearningContentModal from "./AddLearningContentModal";
import * as api from "./api";

vi.mock("./api",()=>({getLearningCatalog:vi.fn(),listLearningBooks:vi.fn(),resolveLearningContent:vi.fn(),createLearningBook:vi.fn()}));
const capability={key:"classical_definition",version:1,name_i18n_key:"文言解释",description_i18n_key:"",local_only:true,languages:["lzh"],subject_kinds:["word"],provider_pipeline:[],allowed_question_types:["text_input"],default_question_types:["text_input"],fields:[{key:"meaning_in_context",type:"text",label_i18n_key:"语境义",help_i18n_key:"",required:true,editable:true}]};

it("renders the selected capability schema and confirms only compatible books",async()=>{
 vi.mocked(api.getLearningCatalog).mockResolvedValue({capabilities:[capability],question_types:[],profiles:[],local_available:true});
 vi.mocked(api.listLearningBooks).mockResolvedValue([{id:"good",name:"古文",description:"",capability_key:"classical_definition",question_types_json:"[]"},{id:"bad",name:"英语",description:"",capability_key:"english_definition",question_types_json:"[]"}]);
 vi.mocked(api.resolveLearningContent).mockResolvedValue({content:{id:""},value:{meaning_in_context:"跑"},source:"classical_chinese_dictionary"});
 render(<AddLearningContentModal value={{capabilityKey:"classical_definition",selection:{text:"走",page:1,context:"双兔傍地走"}}} datasetId="ds" documentId="doc" onClose={()=>{}} onAdded={()=>{}}/>);
 expect(await screen.findByDisplayValue("跑")).toBeInTheDocument();expect(api.resolveLearningContent).toHaveBeenCalledWith(expect.objectContaining({preview:true}));fireEvent.mouseDown(screen.getByRole("combobox",{name:"学习集"}));expect((await screen.findAllByText("古文")).length).toBeGreaterThan(0);expect(screen.queryByText("英语")).not.toBeInTheDocument();fireEvent.click(screen.getByRole("button",{name:"加入学习集"}));await waitFor(()=>expect(api.resolveLearningContent).toHaveBeenLastCalledWith(expect.objectContaining({value:expect.objectContaining({meaning_in_context:"跑"}),book_ids:["good"]})));
});

it("creates a compatible Chinese collection before confirming dictionary content",async()=>{
 const chineseCapability={...capability,key:"chinese_definition",name_i18n_key:"汉语解释",languages:["zh-Hans"],fields:[{key:"definition",type:"text",label_i18n_key:"释义",help_i18n_key:"",required:true,editable:true}]};
 vi.mocked(api.getLearningCatalog).mockResolvedValue({capabilities:[chineseCapability],question_types:[],profiles:[],local_available:true});
 vi.mocked(api.listLearningBooks).mockResolvedValue([]);
 vi.mocked(api.resolveLearningContent).mockResolvedValue({content:{id:"content-zh"},value:{definition:"行走；行动。"},source:"chinese_dictionary"});
 vi.mocked(api.createLearningBook).mockResolvedValue({id:"book-zh",name:"汉语生词",description:"",capability_key:"chinese_definition",question_types_json:'["text_input"]'});
 render(<AddLearningContentModal value={{capabilityKey:"chinese_definition",selection:{text:"行",page:1,context:"行万里路"}}} datasetId="ds" documentId="doc" onClose={()=>{}} onAdded={()=>{}}/>);
 expect(await screen.findByDisplayValue("行走；行动。")).toBeInTheDocument();
 expect(screen.getByText(/chinese_dictionary/)).toBeInTheDocument();
 fireEvent.change(screen.getByPlaceholderText("新学习集名称"),{target:{value:"汉语生词"}});
 fireEvent.click(screen.getByRole("button",{name:"创建兼容学习集"}));
 await waitFor(()=>expect(api.createLearningBook).toHaveBeenCalledWith({name:"汉语生词",capability_key:"chinese_definition",question_types:["text_input"]}));
 fireEvent.click(screen.getByRole("button",{name:"加入学习集"}));
 await waitFor(()=>expect(api.resolveLearningContent).toHaveBeenLastCalledWith(expect.objectContaining({value:expect.objectContaining({definition:"行走；行动。"}),book_ids:["book-zh"]})));
});
