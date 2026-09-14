import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";
import AddLearningContentModal from "./AddLearningContentModal";
import * as api from "./api";

vi.mock("./api",()=>({getLearningCatalog:vi.fn(),listLearningBooks:vi.fn(),resolveLearningContent:vi.fn(),confirmLearningContent:vi.fn()}));
const capability={key:"classical_definition",version:1,name_i18n_key:"文言解释",description_i18n_key:"",local_only:true,languages:["lzh"],subject_kinds:["word"],provider_pipeline:[],allowed_question_types:["text_input"],default_question_types:["text_input"],fields:[{key:"meaning_in_context",type:"text",label_i18n_key:"语境义",help_i18n_key:"",required:true,editable:true}]};

it("renders the selected capability schema and confirms only compatible books",async()=>{
 vi.mocked(api.getLearningCatalog).mockResolvedValue({capabilities:[capability],question_types:[],profiles:[],local_available:true});
 vi.mocked(api.listLearningBooks).mockResolvedValue([{id:"good",name:"古文",description:"",capability_key:"classical_definition",question_types_json:"[]"},{id:"bad",name:"英语",description:"",capability_key:"english_definition",question_types_json:"[]"}]);
 vi.mocked(api.resolveLearningContent).mockResolvedValue({content:{id:"content"},value:{meaning_in_context:"跑"},source:"classical_chinese_dictionary"});vi.mocked(api.confirmLearningContent).mockResolvedValue({});
 render(<AddLearningContentModal value={{capabilityKey:"classical_definition",selection:{text:"走",page:1,context:"双兔傍地走"}}} datasetId="ds" documentId="doc" onClose={()=>{}} onAdded={()=>{}}/>);
 expect(await screen.findByDisplayValue("跑")).toBeInTheDocument();fireEvent.mouseDown(screen.getByRole("combobox",{name:"learning.learningCollections"}));expect((await screen.findAllByText("古文")).length).toBeGreaterThan(0);expect(screen.queryByText("英语")).not.toBeInTheDocument();fireEvent.click(screen.getByRole("button",{name:"learning.addToCollection"}));await waitFor(()=>expect(api.confirmLearningContent).toHaveBeenCalledWith("content",expect.objectContaining({meaning_in_context:"跑"}),["good"]));
});
