import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import * as api from "./api";
import DocumentLearningPanel, { displayPresetKey } from "./DocumentLearningPanel";

vi.mock("./api",async()=>{
  const actual=await vi.importActual<typeof import("./api")>("./api");
  return {...actual,listLearningPresets:vi.fn(),putLearningPreset:vi.fn(),updateLearningPreset:vi.fn(),deleteLearningPreset:vi.fn(),getLatestPreanalysisTask:vi.fn(),getPreanalysisTask:vi.fn(),createPreanalysisTask:vi.fn(),runPreanalysisTask:vi.fn(),listPreanalysisDrafts:vi.fn()};
});

const capability={key:"chinese_definition",version:1,name_i18n_key:"learning.capability.chineseDefinition.name",description_i18n_key:"",local_only:true,languages:["zh-Hans"],subject_kinds:["word"],fields:[{key:"meaning_in_context",type:"text",label_i18n_key:"learning.field.meaningInContext",help_i18n_key:"",required:true,editable:true}],provider_pipeline:["llm"],allowed_question_types:[],default_question_types:[],cache_policy:{default_scope:"document",allowed_scopes:["document"],context_sensitive:true}};

beforeEach(()=>{
  vi.clearAllMocks();
  vi.mocked(api.listLearningPresets).mockResolvedValue([{id:"p1",scope_type:"document",scope_id:"doc",document_revision:"r1",capability_key:"chinese_definition",normalized_key:"安全距离",value_json:'{"meaning_in_context":"车辆安全行驶所需的间隔"}',origin:"user",status:"published",priority:0,user_edited:true}]);
  vi.mocked(api.putLearningPreset).mockResolvedValue({});
  vi.mocked(api.getLatestPreanalysisTask).mockResolvedValue(undefined);
});

it("closes the dialog after starting and shows progress below a disabled action",async()=>{
  vi.mocked(api.createPreanalysisTask).mockResolvedValue({id:"task",status:"queued",total:2,completed:0,failed:0,result_json:"[]"});
  vi.mocked(api.runPreanalysisTask).mockResolvedValue({id:"task",status:"queued",total:2,completed:0,failed:0,result_json:"[]"});
  vi.mocked(api.getPreanalysisTask).mockResolvedValue({id:"task",status:"running",total:2,completed:1,failed:0,result_json:"[]"});
  render(<DocumentLearningPanel datasetId="ds" documentId="doc" revision="r1" capabilities={[capability]} localAvailable/>);
  fireEvent.click(await screen.findByRole("button",{name:/AI 分析文档/}));
  fireEvent.click(await screen.findByRole("button",{name:"开始分析"}));
  await waitFor(()=>expect(screen.getByRole("dialog",{name:"AI 分析文档"})).toHaveClass("ant-zoom-leave"));
  expect(screen.getByRole("button",{name:/AI 分析文档/})).toBeDisabled();
  expect(await screen.findByText(/状态：running，成功 1，失败 0/)).toBeInTheDocument();
});

it("restores a partially successful task and exposes all successful drafts",async()=>{
  vi.mocked(api.getLatestPreanalysisTask).mockResolvedValue({id:"partial",status:"completed_with_errors",total:3,completed:2,failed:1,result_json:"[]"});
  vi.mocked(api.listPreanalysisDrafts).mockResolvedValue({presets:[{id:"ok",scope_type:"document",scope_id:"doc",document_revision:"r1",capability_key:"chinese_definition",normalized_key:"急弯",value_json:'{"meaning_in_context":"方向急剧变化的弯道"}',origin:"llm_preanalysis",status:"draft",priority:0,user_edited:false}],contents:[]});
  render(<DocumentLearningPanel datasetId="ds" documentId="doc" revision="r1" capabilities={[capability]} localAvailable/>);
  const button=await screen.findByRole("button",{name:/AI 分析文档/});
  await waitFor(()=>expect(button).toBeEnabled());
  fireEvent.click(button);
  expect(await screen.findByText("急弯")).toBeInTheDocument();
  expect(screen.getByText("方向急剧变化的弯道")).toBeInTheDocument();
});

it("shows source text instead of an internal generated cache key",()=>{
  expect(displayPresetKey("learning-v1\x1fchinese_definition\x1f1\x1f1\x1f急弯\x1fdafb5d8c130fea84")).toBe("急弯");
  expect(displayPresetKey("安全距离")).toBe("安全距离");
});

it("shows existing answers first and creates schema fields without exposing JSON",async()=>{
  render(<DocumentLearningPanel datasetId="ds" documentId="doc" revision="r1" capabilities={[capability]} localAvailable/>);
  expect(await screen.findByText("安全距离")).toBeInTheDocument();
  expect(screen.getByText("车辆安全行驶所需的间隔")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button",{name:/新增回答/}));
  expect(await screen.findByLabelText("匹配内容")).toBeInTheDocument();
  expect(screen.getByLabelText("语境义")).toBeInTheDocument();
  expect(screen.queryByText(/JSON/)).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("匹配内容"),{target:{value:"制动距离"}});
  fireEvent.change(screen.getByLabelText("语境义"),{target:{value:"车辆制动到停止经过的距离"}});
  fireEvent.click(screen.getByRole("button",{name:/保\s*存/}));
  await waitFor(()=>expect(api.putLearningPreset).toHaveBeenCalledWith(expect.objectContaining({scope_type:"document",scope_id:"doc",key:"制动距离",value:{meaning_in_context:"车辆制动到停止经过的距离"}})));
});
