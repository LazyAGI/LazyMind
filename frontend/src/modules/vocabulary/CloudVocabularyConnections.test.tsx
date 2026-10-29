import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import CloudVocabularyConnections from "./CloudVocabularyConnections";
import * as api from "./cloudApi";
vi.mock("react-i18next", () => ({useTranslation:()=>({t:(key:string)=>key.replace("vocabularyCloud.","")})}));
vi.mock("./cloudApi",()=>({listCloudConnections:vi.fn(),listCloudOperations:vi.fn(),saveCloudConnection:vi.fn(),testCloudConnection:vi.fn(),createCloudWordbook:vi.fn(),disconnectCloudConnection:vi.fn(),syncCloudVocabulary:vi.fn()}));
const connection={provider:"maimemo" as const,configured:true,enabled:false,target_id:"target",target_name:"My book",add_to_study:false,pending:0,failed:0,succeeded:0,last_error:""};
async function open(provider="maimemo"){fireEvent.click(await screen.findByRole("button",{name:`${provider} manage`}));return await screen.findByRole("region",{name:provider});}
describe("cloud vocabulary connections",()=>{
  beforeEach(()=>{vi.clearAllMocks();vi.mocked(api.listCloudConnections).mockResolvedValue([connection]);vi.mocked(api.listCloudOperations).mockResolvedValue([]);vi.mocked(api.testCloudConnection).mockResolvedValue([{id:"target",name:"My book"}]);vi.mocked(api.saveCloudConnection).mockImplementation(async(provider,body)=>({...connection,...body,provider,configured:true}));});
  it("keeps both entries on load failure and never exposes an unknown configuration",async()=>{
    vi.mocked(api.listCloudConnections).mockRejectedValueOnce(new Error("unavailable"));render(<CloudVocabularyConnections/>);
    fireEvent.click(await screen.findByRole("button",{name:"maimemo manage"}));
    const dialog=await screen.findByRole("dialog");expect(await within(dialog).findByText("loadFailed")).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("token")).toBeNull();expect(api.saveCloudConnection).not.toHaveBeenCalled();
    expect(screen.getByRole("button",{name:"eudic manage"})).toBeEnabled();
  });
  it("loads saved credentials automatically and selects the only wordbook without writing",async()=>{
    vi.mocked(api.listCloudConnections).mockResolvedValue([{...connection,target_id:"",target_name:""}]);render(<CloudVocabularyConnections/>);const card=await open();
    await waitFor(()=>expect(within(card).getByRole("button",{name:"saveAndEnable"})).toBeEnabled());
    expect(api.testCloudConnection).toHaveBeenCalledWith("maimemo",undefined);
    expect(within(card).getByText("My book")).toBeInTheDocument();
    expect(api.saveCloudConnection).not.toHaveBeenCalled();expect(api.createCloudWordbook).not.toHaveBeenCalled();expect(api.syncCloudVocabulary).not.toHaveBeenCalled();
    fireEvent.click(within(card).getByRole("button",{name:"saveAndEnable"}));
    await waitFor(()=>expect(api.saveCloudConnection).toHaveBeenCalledWith("maimemo",{enabled:true,target_id:"target",target_name:"My book",add_to_study:false}));
    expect(await within(card).findByText("savedEnabled")).toBeInTheDocument();
  });
  it("requires verified credentials and a target before saving a new connection",async()=>{
    vi.mocked(api.listCloudConnections).mockResolvedValue([]);vi.mocked(api.testCloudConnection).mockResolvedValueOnce([]);
    render(<CloudVocabularyConnections/>);const card=await open();
    fireEvent.change(within(card).getByLabelText("token"),{target:{value:"secret"}});
    expect(within(card).queryByRole("button",{name:"saveAndEnable"})).toBeNull();
    fireEvent.click(within(card).getByRole("button",{name:"verify"}));
    await within(card).findByText("noBooks");
    expect(within(card).getByRole("button",{name:"saveAndEnable"})).toBeDisabled();
    expect(api.saveCloudConnection).not.toHaveBeenCalled();expect(api.createCloudWordbook).not.toHaveBeenCalled();
  });
  it("verifies a new token, saves enabled, clears credentials and defaults study off",async()=>{
    vi.mocked(api.listCloudConnections).mockResolvedValue([]);render(<CloudVocabularyConnections/>);const card=await open();
    fireEvent.change(within(card).getByLabelText("token"),{target:{value:"secret"}});fireEvent.click(within(card).getByRole("button",{name:"verify"}));
    await waitFor(()=>expect(within(card).getByRole("button",{name:"saveAndEnable"})).toBeEnabled());
    fireEvent.click(within(card).getByRole("button",{name:"saveAndEnable"}));
    await waitFor(()=>expect(api.saveCloudConnection).toHaveBeenCalledWith("maimemo",{token:"secret",enabled:true,target_id:"target",target_name:"My book",add_to_study:false}));
    expect(await within(card).findByText("savedEnabled")).toBeInTheDocument();expect(within(card).queryByDisplayValue("secret")).toBeNull();
  });
  it("persists the sync switch immediately and keeps the saved state on failure",async()=>{
    vi.mocked(api.listCloudConnections).mockResolvedValue([{...connection,enabled:true}]);vi.mocked(api.saveCloudConnection).mockRejectedValueOnce(new Error("secret upstream"));
    render(<CloudVocabularyConnections/>);const card=await open();await waitFor(()=>expect(within(card).getByRole("switch")).toBeEnabled());
    fireEvent.click(within(card).getByRole("switch"));
    expect(await within(card).findByText("actionFailed")).toBeInTheDocument();expect(within(card).getByRole("switch")).toBeChecked();expect(screen.queryByText(/secret upstream/)).toBeNull();
    fireEvent.click(within(card).getByRole("switch"));
    expect(await within(card).findByText("savedPaused")).toBeInTheDocument();expect(within(card).getByRole("switch")).not.toBeChecked();
  });
  it("does not persist token drafts through the sync switch",async()=>{
    render(<CloudVocabularyConnections/>);const card=await open();await waitFor(()=>expect(within(card).getByRole("switch")).toBeEnabled());
    fireEvent.click(within(card).getByRole("switch"));
    await waitFor(()=>expect(api.saveCloudConnection).toHaveBeenCalledWith("maimemo",{enabled:true,target_id:"target",target_name:"My book",add_to_study:false}));
    fireEvent.click(within(card).getByText("advanced"));fireEvent.click(within(card).getByText("changeToken"));fireEvent.change(within(card).getByLabelText("token"),{target:{value:"draft-secret"}});
    expect(within(card).queryByRole("switch")).toBeNull();expect(api.saveCloudConnection).toHaveBeenCalledTimes(1);
  });
  it("reflects remotely changed clean configuration without saving stale consent",async()=>{
    vi.useFakeTimers();const view=render(<CloudVocabularyConnections/>);
    try{await act(async()=>{});fireEvent.click(screen.getByRole("button",{name:"maimemo manage"}));await act(async()=>{});const card=screen.getByRole("region",{name:"maimemo"});
      vi.mocked(api.listCloudConnections).mockResolvedValue([{...connection,enabled:true,target_id:"remote",target_name:"Remote book",add_to_study:true}]);
      await act(async()=>{await vi.advanceTimersByTimeAsync(10000);});
      expect(within(card).getByRole("switch")).toBeChecked();expect(within(card).getByText("Remote book")).toBeInTheDocument();expect(within(card).queryByText("configConflict")).toBeNull();expect(api.saveCloudConnection).not.toHaveBeenCalled();
    }finally{view.unmount();vi.useRealTimers();}
  });
  it("translates persisted errors without exposing unknown text",async()=>{
    vi.mocked(api.listCloudConnections).mockResolvedValue([{...connection,last_error:"cloud vocabulary: invalid_token"}]);vi.mocked(api.listCloudOperations).mockResolvedValue([{id:"1",provider:"maimemo",term:"word",status:"failed",last_error:"secret upstream",updated_at:"2026-10-29T00:00:00Z"}]);
    render(<CloudVocabularyConnections/>);const card=await open();expect(await within(card).findByText("errors.invalid_token")).toBeInTheDocument();expect(within(card).getByText("errors.sync_failed")).toBeInTheDocument();expect(screen.queryByText("secret upstream")).toBeNull();
  });
  it("guides Eudic authorization, translates rejection, and opens creation for an empty English list",async()=>{
    vi.mocked(api.listCloudConnections).mockResolvedValue([]);
    vi.mocked(api.testCloudConnection).mockRejectedValueOnce({response:{data:{code:2005301,message:"sensitive"}}}).mockResolvedValueOnce([]);
    render(<CloudVocabularyConnections/>);const card=await open("eudic");
    expect(within(card).getByRole("link",{name:"eudicTokenLink"})).toHaveAttribute("href","https://my.eudic.net/OpenAPI/Authorization");
    expect(within(card).getByText("eudicTokenHelp")).toBeInTheDocument();
    fireEvent.change(within(card).getByLabelText("token"),{target:{value:"NIS complete-token"}});fireEvent.click(within(card).getByRole("button",{name:"verify"}));
    expect(await within(card).findByText("errors.invalid_token")).toBeInTheDocument();
    fireEvent.click(within(card).getByRole("button",{name:"verify"}));await within(card).findByText("noBooks");
    expect(within(card).getByText("createTarget").closest("details")).toHaveAttribute("open");
    expect(api.testCloudConnection).toHaveBeenLastCalledWith("eudic","NIS complete-token");expect(api.createCloudWordbook).not.toHaveBeenCalled();
  });
  it("offers visible stored-credential retry and searches English wordbook names",async()=>{
    vi.mocked(api.listCloudConnections).mockResolvedValue([{...connection,provider:"eudic",target_id:"",target_name:""}]);
    vi.mocked(api.testCloudConnection).mockRejectedValueOnce({response:{data:{code:2005304}}}).mockResolvedValueOnce([{id:"a",name:"Travel"},{id:"b",name:"Science"}]);
    render(<CloudVocabularyConnections/>);const card=await open("eudic");
    expect(await within(card).findByText("errors.rate_limited")).toBeInTheDocument();
    fireEvent.click(within(card).getByRole("button",{name:"refreshBooks"}));
    await waitFor(()=>expect(within(card).getByRole("button",{name:"refreshBooks"})).toBeEnabled());
    expect(within(card).getByText("eudicEnglishHelp")).toBeInTheDocument();
    const picker=within(card).getByRole("combobox");fireEvent.mouseDown(picker);fireEvent.change(picker,{target:{value:"Science"}});
    await waitFor(()=>expect(screen.getAllByText("Science").length).toBeGreaterThan(0));expect(screen.queryByText("Travel")).toBeNull();
    expect(api.saveCloudConnection).not.toHaveBeenCalled();
  });
  it("does not overlap polling or continue scheduling after unmount",async()=>{
    vi.useFakeTimers();let finish!:(value:typeof connection[])=>void;vi.mocked(api.listCloudConnections).mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve;}));const view=render(<CloudVocabularyConnections/>);
    try{await act(async()=>{await vi.advanceTimersByTimeAsync(30000);});expect(api.listCloudConnections).toHaveBeenCalledTimes(1);await act(async()=>{finish([connection]);});view.unmount();await act(async()=>{await vi.advanceTimersByTimeAsync(30000);});expect(api.listCloudConnections).toHaveBeenCalledTimes(1);}finally{view.unmount();vi.useRealTimers();}
  });
  it("preserves edits on stats refresh but blocks remote configuration conflicts until reload",async()=>{
    vi.useFakeTimers();const view=render(<CloudVocabularyConnections/>);
    try{await act(async()=>{});fireEvent.click(screen.getByRole("button",{name:"maimemo manage"}));await act(async()=>{});const card=screen.getByRole("region",{name:"maimemo"});
      fireEvent.click(within(card).getByText("advanced"));fireEvent.click(within(card).getByText("changeToken"));fireEvent.change(within(card).getByLabelText("token"),{target:{value:"draft-secret"}});
      vi.mocked(api.listCloudConnections).mockResolvedValue([{...connection,pending:4}]);await act(async()=>{await vi.advanceTimersByTimeAsync(10000);});expect(within(card).getByLabelText("token")).toHaveValue("draft-secret");expect(within(card).queryByText("configConflict")).toBeNull();
      vi.mocked(api.listCloudConnections).mockResolvedValue([{...connection,enabled:true}]);await act(async()=>{await vi.advanceTimersByTimeAsync(10000);});expect(within(card).getByText("configConflict")).toBeInTheDocument();expect(api.saveCloudConnection).not.toHaveBeenCalled();
      fireEvent.click(within(card).getByRole("button",{name:"reloadSettings"}));expect(within(card).queryByDisplayValue("draft-secret")).toBeNull();expect(within(card).getByRole("switch")).toBeChecked();
    }finally{view.unmount();vi.useRealTimers();}
  });
});
