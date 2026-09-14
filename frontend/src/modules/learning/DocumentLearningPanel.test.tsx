import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import * as api from "./api";
import DocumentLearningPanel from "./DocumentLearningPanel";

vi.mock("./api",async()=>{
  const actual=await vi.importActual<typeof import("./api")>("./api");
  return {...actual,listLearningPresets:vi.fn(),putLearningPreset:vi.fn(),updateLearningPreset:vi.fn(),deleteLearningPreset:vi.fn()};
});

const capability={key:"chinese_definition",version:1,name_i18n_key:"learning.capability.chineseDefinition.name",description_i18n_key:"",local_only:true,languages:["zh-Hans"],subject_kinds:["word"],fields:[{key:"meaning_in_context",type:"text",label_i18n_key:"learning.field.meaningInContext",help_i18n_key:"",required:true,editable:true}],provider_pipeline:["llm"],allowed_question_types:[],default_question_types:[],cache_policy:{default_scope:"document",allowed_scopes:["document"],context_sensitive:true}};

beforeEach(()=>{
  vi.clearAllMocks();
  vi.mocked(api.listLearningPresets).mockResolvedValue([{id:"p1",scope_type:"document",scope_id:"doc",document_revision:"r1",capability_key:"chinese_definition",normalized_key:"安全距离",value_json:'{"meaning_in_context":"车辆安全行驶所需的间隔"}',origin:"user",status:"published",priority:0,user_edited:true}]);
  vi.mocked(api.putLearningPreset).mockResolvedValue({});
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
