import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import * as api from "./api";
import DocumentLearningPanel, { capabilitySupportsAnalysisText, displayPresetKey, presetDisplayValue } from "./DocumentLearningPanel";

vi.mock("./api",async()=>{
  const actual=await vi.importActual<typeof import("./api")>("./api");
  return {...actual,listLearningPresets:vi.fn(),putLearningPreset:vi.fn(),updateLearningPreset:vi.fn(),deleteLearningPreset:vi.fn(),getLatestPreanalysisTask:vi.fn(),getPreanalysisTask:vi.fn(),createPreanalysisTask:vi.fn(),runPreanalysisTask:vi.fn(),cancelPreanalysisTask:vi.fn(),listPreanalysisDrafts:vi.fn(),publishPreanalysisDrafts:vi.fn()};
});

const capability={key:"chinese_definition",version:1,name_i18n_key:"learning.capability.chineseDefinition.name",description_i18n_key:"",local_only:true,languages:["zh-Hans"],subject_kinds:["word"],fields:[{key:"meaning_in_context",type:"text",label_i18n_key:"learning.field.meaningInContext",help_i18n_key:"",required:true,editable:true}],provider_pipeline:["llm"],allowed_question_types:[],default_question_types:[],cache_policy:{default_scope:"document",allowed_scopes:["document"],context_sensitive:true}};

beforeEach(()=>{
  vi.clearAllMocks();
  vi.mocked(api.listLearningPresets).mockResolvedValue([{id:"p1",scope_type:"document",scope_id:"doc",document_revision:"r1",capability_key:"chinese_definition",normalized_key:"安全距离",value_json:'{"meaning_in_context":"车辆安全行驶所需的间隔"}',origin:"user",status:"published",priority:0,user_edited:true}]);
  vi.mocked(api.putLearningPreset).mockResolvedValue({});
  vi.mocked(api.deleteLearningPreset).mockResolvedValue({});
  vi.mocked(api.publishPreanalysisDrafts).mockResolvedValue({});
  vi.mocked(api.getLatestPreanalysisTask).mockResolvedValue(undefined);
});

it("closes the dialog after starting and shows progress below a disabled action",async()=>{
  vi.mocked(api.createPreanalysisTask).mockResolvedValue({id:"task",status:"queued",total:2,completed:0,failed:0,result_json:"[]",error_message:""});
  vi.mocked(api.runPreanalysisTask).mockResolvedValue({id:"task",status:"queued",total:2,completed:0,failed:0,result_json:"[]",error_message:""});
  vi.mocked(api.getPreanalysisTask).mockResolvedValue({id:"task",status:"running",total:2,completed:1,failed:0,result_json:"[]",error_message:""});
  render(<DocumentLearningPanel datasetId="ds" documentId="doc" revision="r1" capabilities={[capability]} localAvailable/>);
  fireEvent.click(await screen.findByRole("button",{name:/AI 生成/}));
  fireEvent.click(await screen.findByRole("button",{name:"开始生成"}));
  await waitFor(()=>expect(screen.getByRole("dialog",{name:"AI 生成速查内容"})).toHaveClass("ant-zoom-leave"));
  expect(screen.getByRole("button",{name:/AI 生成/})).toBeDisabled();
  expect(await screen.findByText(/状态：running，成功 1，失败 0/)).toBeInTheDocument();
});

it("sends a user focus and only the selected paragraphs for local analysis",async()=>{
  vi.mocked(api.createPreanalysisTask).mockResolvedValue({id:"local",status:"queued",total:2,completed:0,failed:0,result_json:"[]",error_message:""});
  vi.mocked(api.runPreanalysisTask).mockResolvedValue({id:"local",status:"queued",total:2,completed:0,failed:0,result_json:"[]",error_message:""});
  render(<DocumentLearningPanel datasetId="ds" documentId="doc" revision="r1" capabilities={[capability]} localAvailable analysisSelection={{selections:[{text:"第一段",context:"第一段上下文",page:3},{text:"第二段",context:"第二段上下文",page:4}],requestId:1}}/>);
  const dialog=await screen.findByRole("dialog",{name:"AI 生成速查内容"});
  const selectedText=dialog.querySelector("textarea[readonly]") as HTMLTextAreaElement|null;
  expect(selectedText?.value).toBe("第一段\n\n第二段");
  fireEvent.change(screen.getByPlaceholderText(/重点提取考试易错概念/),{target:{value:"只分析道路安全风险"}});
  fireEvent.click(screen.getByRole("button",{name:"开始生成"}));
  await waitFor(()=>expect(api.createPreanalysisTask).toHaveBeenCalledWith(expect.objectContaining({
    analysis_direction:"只分析道路安全风险",
    items:[{text:"第一段",context:"第一段上下文",page:3},{text:"第二段",context:"第二段上下文",page:4}],
  })));
});

it("restores a partially successful task and exposes all successful drafts",async()=>{
  vi.mocked(api.getLatestPreanalysisTask).mockResolvedValue({id:"partial",status:"completed_with_errors",total:3,completed:2,failed:1,result_json:"[]",error_message:"content resolution[chinese_definition] \"急弯\": model response is invalid"});
  vi.mocked(api.listPreanalysisDrafts).mockResolvedValue({presets:[{id:"ok",scope_type:"document",scope_id:"doc",document_revision:"r1",capability_key:"chinese_definition",normalized_key:"急弯",value_json:'{"meaning_in_context":"方向急剧变化的弯道"}',origin:"llm_preanalysis",status:"draft",priority:0,user_edited:false}],contents:[]});
  render(<DocumentLearningPanel datasetId="ds" documentId="doc" revision="r1" capabilities={[capability]} localAvailable/>);
  await waitFor(()=>expect(screen.getByRole("button",{name:/AI 生成/})).toBeEnabled());
  expect(await screen.findByText("待确认内容（1）")).toBeInTheDocument();
  expect(await screen.findByText("急弯")).toBeInTheDocument();
  expect(screen.getByText("方向急剧变化的弯道")).toBeInTheDocument();
});

it("publishes selected drafts from the side panel and keeps remaining drafts visible",async()=>{
  const first={id:"one",scope_type:"document",scope_id:"doc",document_revision:"r1",capability_key:"chinese_definition",normalized_key:"急弯",value_json:'{"meaning_in_context":"急转弯"}',origin:"llm_preanalysis",status:"draft",priority:0,user_edited:false};
  const second={...first,id:"two",normalized_key:"坡道",value_json:'{"meaning_in_context":"倾斜道路"}'};
  vi.mocked(api.getLatestPreanalysisTask).mockResolvedValue({id:"done",status:"completed",total:2,completed:2,failed:0,result_json:"[]",error_message:""});
  vi.mocked(api.listPreanalysisDrafts).mockResolvedValueOnce({presets:[first,second],contents:[]}).mockResolvedValueOnce({presets:[second],contents:[]});
  render(<DocumentLearningPanel datasetId="ds" documentId="doc" revision="r1" capabilities={[capability]} localAvailable/>);
  expect(await screen.findByText("待确认内容（2）")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("checkbox",{name:/急弯/}));
  fireEvent.click(screen.getByRole("button",{name:"确认启用（1）"}));
  await waitFor(()=>expect(api.publishPreanalysisDrafts).toHaveBeenCalledWith("done","r1",["two"],[]));
  expect(await screen.findByText("待确认内容（1）")).toBeInTheDocument();
  expect(screen.getByText("坡道")).toBeInTheDocument();
});

it("stops a running analysis and restores the analysis action",async()=>{
  vi.mocked(api.getLatestPreanalysisTask).mockResolvedValue({id:"running",status:"running",total:4,completed:1,failed:0,result_json:"[]",error_message:""});
  vi.mocked(api.getPreanalysisTask).mockResolvedValue({id:"running",status:"running",total:4,completed:1,failed:0,result_json:"[]",error_message:""});
  vi.mocked(api.cancelPreanalysisTask).mockResolvedValue({id:"running",status:"canceled",total:4,completed:1,failed:0,result_json:"[]",error_message:""});
  render(<DocumentLearningPanel datasetId="ds" documentId="doc" revision="r1" capabilities={[capability]} localAvailable/>);
  fireEvent.click(await screen.findByText("停止生成"));
  const stopButtons=await screen.findAllByRole("button",{name:"停止生成"});
  fireEvent.click(stopButtons[stopButtons.length-1]);
  await waitFor(()=>expect(api.cancelPreanalysisTask).toHaveBeenCalledWith("running"));
  await waitFor(()=>expect(screen.queryByRole("button",{name:"停止生成"})).not.toBeInTheDocument());
  expect(screen.getByRole("button",{name:/AI 生成/})).toBeEnabled();
});

it("shows source text instead of an internal generated cache key",()=>{
  expect(displayPresetKey("learning-v1\x1fchinese_definition\x1f1\x1f1\x1f急弯\x1fdafb5d8c130fea84")).toBe("急弯");
  expect(displayPresetKey("安全距离")).toBe("安全距离");
});

it("uses the semantic meaning in the list and keeps examples for hover details",()=>{
  expect(presetDisplayValue('{"pinyin":"kē mù yī","meaning_in_context":"机动车驾驶证考试的第一部分","examples":["科目一满分100分。","考前认真复习题库。"]}')).toEqual({
    semantic:"机动车驾驶证考试的第一部分",
    dictionaryMeaning:"",
    examples:["科目一满分100分。","考前认真复习题库。"],
  });
});

it("keeps the current-context meaning separate from the dictionary meaning",()=>{
  expect(presetDisplayValue('{"meaning_in_context":"股票上涨途中测试上方压力的长上影线形态","dictionary_meaning":"一种武术招式名称","examples":["该股出现仙人指路形态。"]}')).toEqual({
    semantic:"股票上涨途中测试上方压力的长上影线形态",
    dictionaryMeaning:"一种武术招式名称",
    examples:["该股出现仙人指路形态。"],
  });
});

it("filters paragraph analysis capabilities by the selected text language",()=>{
  const english={...capability,key:"english_definition",languages:["en"],subject_kinds:["word","phrase"]};
  expect(capabilitySupportsAnalysisText(capability,"Fixed appending-stream reader state.")).toBe(false);
  expect(capabilitySupportsAnalysisText(english,"Fixed appending-stream reader state.")).toBe(true);
  expect(capabilitySupportsAnalysisText(capability,"道路交通安全知识")).toBe(true);
});

it("shows existing answers first and creates schema fields without exposing JSON",async()=>{
  render(<DocumentLearningPanel datasetId="ds" documentId="doc" revision="r1" capabilities={[capability]} localAvailable/>);
  expect(await screen.findByText("安全距离")).toBeInTheDocument();
  expect(screen.getByText("车辆安全行驶所需的间隔")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button",{name:/添加速查项/}));
  expect(await screen.findByLabelText("查询内容")).toBeInTheDocument();
  expect(screen.getByLabelText("语境义")).toBeInTheDocument();
  expect(screen.queryByText(/JSON/)).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("查询内容"),{target:{value:"制动距离"}});
  fireEvent.change(screen.getByLabelText("语境义"),{target:{value:"车辆制动到停止经过的距离"}});
  fireEvent.click(screen.getByRole("button",{name:/保\s*存/}));
  await waitFor(()=>expect(api.putLearningPreset).toHaveBeenCalledWith(expect.objectContaining({scope_type:"document",scope_id:"doc",key:"制动距离",value:{meaning_in_context:"车辆制动到停止经过的距离"}})));
});

it("selects visible preset answers and deletes them in bulk",async()=>{
  vi.mocked(api.listLearningPresets).mockResolvedValueOnce([
    {id:"p1",scope_type:"document",scope_id:"doc",document_revision:"r1",capability_key:"chinese_definition",normalized_key:"安全距离",value_json:'{"meaning_in_context":"车辆安全行驶所需的间隔"}',origin:"user",status:"published",priority:0,user_edited:true},
    {id:"p2",scope_type:"document",scope_id:"doc",document_revision:"r1",capability_key:"chinese_definition",normalized_key:"制动距离",value_json:'{"meaning_in_context":"车辆制动至停止的距离"}',origin:"user",status:"published",priority:0,user_edited:true},
  ]).mockResolvedValue([]);
  render(<DocumentLearningPanel datasetId="ds" documentId="doc" revision="r1" capabilities={[capability]} localAvailable/>);
  fireEvent.click(await screen.findByRole("checkbox",{name:/选择当前列表/}));
  fireEvent.click(screen.getByRole("button",{name:"批量删除（2）"}));
  fireEvent.click(await screen.findByRole("button",{name:"删 除"}));
  await waitFor(()=>expect(api.deleteLearningPreset).toHaveBeenCalledTimes(2));
  expect(api.deleteLearningPreset).toHaveBeenCalledWith("p1");
  expect(api.deleteLearningPreset).toHaveBeenCalledWith("p2");
});
