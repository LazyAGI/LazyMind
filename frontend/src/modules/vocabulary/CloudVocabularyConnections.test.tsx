import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import CloudVocabularyConnections from "./CloudVocabularyConnections";
import * as api from "./cloudApi";
vi.mock("react-i18next", () => ({useTranslation:()=>({t:(key:string)=>key.replace("vocabularyCloud.","")})}));
vi.mock("./cloudApi",()=>({listCloudConnections:vi.fn(),listCloudOperations:vi.fn(async()=>[]),saveCloudConnection:vi.fn(),testCloudConnection:vi.fn(async()=>[{id:"target",name:"LazyMind 生词本"}]),createCloudWordbook:vi.fn(),disconnectCloudConnection:vi.fn(),syncCloudVocabulary:vi.fn()}));
const connection={provider:"eudic" as const,configured:true,enabled:false,target_id:"target",target_name:"LazyMind 生词本",add_to_study:false,pending:0,failed:0,succeeded:0,last_error:""};
describe("cloud vocabulary connections",()=>{
  beforeEach(()=>{vi.clearAllMocks();vi.mocked(api.listCloudConnections).mockResolvedValue([connection]);vi.mocked(api.saveCloudConnection).mockImplementation(async(provider,body)=>({...connection,...body,provider,configured:true}));});
  it("keeps both management entries visible on initial load failure without exposing an unsafe form",async()=>{
    vi.mocked(api.listCloudConnections).mockRejectedValueOnce(new Error("unavailable"));
    render(<CloudVocabularyConnections/>);
    const manage=await screen.findByRole("button",{name:"eudic manage"});
    expect(screen.getByRole("button",{name:"maimemo manage"})).toBeEnabled();
    fireEvent.click(manage);
    const dialog=await screen.findByRole("dialog");
    expect(await within(dialog).findByText("loadFailed")).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("token")).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("button",{name:"save"})).not.toBeInTheDocument();
  });
  it("retains stored credentials when saving a blank token and prevents backfill while disabled",async()=>{
    render(<CloudVocabularyConnections/>);
    fireEvent.click(await screen.findByRole("button",{name:"eudic manage"}));
    const card=await screen.findByRole("region",{name:"eudic"});
    await waitFor(()=>expect(within(card).getByRole("button",{name:"save"})).toBeEnabled());
    expect(within(card).getByRole("button",{name:"sync"})).toBeDisabled();
    fireEvent.click(within(card).getByRole("button",{name:"save"}));
    await waitFor(()=>expect(api.saveCloudConnection).toHaveBeenCalledWith("eudic",{enabled:false,target_id:"target",target_name:"LazyMind 生词本",add_to_study:false}));
  });
  it("requires credentials and a target before enabling a new connection",async()=>{
    vi.mocked(api.listCloudConnections).mockResolvedValue([]);
    render(<CloudVocabularyConnections/>);
    fireEvent.click(await screen.findByRole("button",{name:"maimemo manage"}));
    const card=await screen.findByRole("region",{name:"maimemo"});
    expect(within(card).getByRole("switch")).toBeDisabled();
    expect(within(card).getByRole("button",{name:"test"})).toBeDisabled();
    fireEvent.change(within(card).getByLabelText("token"),{target:{value:"secret"}});
    fireEvent.click(within(card).getByRole("button",{name:"test"}));
    await waitFor(()=>expect(within(card).getByRole("switch")).toBeEnabled());
    expect(within(card).getByRole("checkbox")).not.toBeChecked();
    fireEvent.click(within(card).getByRole("switch"));
    fireEvent.click(within(card).getByRole("button",{name:"save"}));
    await waitFor(()=>expect(api.saveCloudConnection).toHaveBeenCalledWith("maimemo",{token:"secret",enabled:true,target_id:"target",target_name:"LazyMind 生词本",add_to_study:false}));
    await waitFor(()=>expect(within(card).getByLabelText("token")).toHaveValue(""));
  });
  it("surfaces connection failures without echoing credentials",async()=>{
    vi.mocked(api.testCloudConnection).mockRejectedValueOnce(new Error("secret-token upstream"));
    render(<CloudVocabularyConnections/>);
    fireEvent.click(await screen.findByRole("button",{name:"eudic manage"}));
    const card=await screen.findByRole("region",{name:"eudic"});
    fireEvent.click(within(card).getByRole("button",{name:"test"}));
    expect(await within(card).findByText("actionFailed")).toBeInTheDocument();
    expect(screen.queryByText(/secret-token/)).not.toBeInTheDocument();
    expect(within(card).getByRole("button",{name:"save"})).toBeEnabled();
  });
  it("does not overlap polling requests and stops scheduling after unmount",async()=>{
    vi.useFakeTimers();
    let finish!: (value:typeof connection[])=>void;
    vi.mocked(api.listCloudConnections).mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve;}));
    const view=render(<CloudVocabularyConnections/>);
    try {
      await act(async()=>{await vi.advanceTimersByTimeAsync(30000);});
      expect(api.listCloudConnections).toHaveBeenCalledTimes(1);
      await act(async()=>{finish([connection]);});
      view.unmount();
      await act(async()=>{await vi.advanceTimersByTimeAsync(30000);});
      expect(api.listCloudConnections).toHaveBeenCalledTimes(1);
    } finally {view.unmount();vi.useRealTimers();}
  });

  it("translates known status errors and hides unknown upstream messages", async()=>{
    vi.mocked(api.listCloudConnections).mockResolvedValue([{...connection,last_error:"cloud vocabulary: invalid_token"}]);
    vi.mocked(api.listCloudOperations).mockResolvedValueOnce([
      {id:"1",provider:"eudic",term:"word",status:"failed",last_error:"vocabulary cloud credentials unavailable",updated_at:"2026-10-29T00:00:00Z"},
      {id:"2",provider:"eudic",term:"word2",status:"failed",last_error:"secret upstream details",updated_at:"2026-10-29T00:00:00Z"},
    ]);
    render(<CloudVocabularyConnections/>);
    fireEvent.click(await screen.findByRole("button",{name:"eudic manage"}));
    expect(await screen.findByText("errors.invalid_token")).toBeInTheDocument();
    expect(screen.getByText("errors.credentials_unavailable")).toBeInTheDocument();
    expect(screen.getByText("errors.sync_failed")).toBeInTheDocument();
    expect(screen.queryByText("secret upstream details")).not.toBeInTheDocument();
  });

  it("keeps unsaved form edits while polling refreshes sync counts", async()=>{
    vi.useFakeTimers();
    vi.mocked(api.listCloudConnections).mockResolvedValueOnce([connection]).mockResolvedValue([{...connection,pending:4}]);
    const view=render(<CloudVocabularyConnections/>);
    try {
      await act(async()=>{});
      fireEvent.click(screen.getByRole("button",{name:"eudic manage"}));
      const card=screen.getByRole("region",{name:"eudic"});
      fireEvent.click(within(card).getByRole("switch"));
      fireEvent.change(within(card).getByLabelText("newTarget"),{target:{value:"My edited wordbook"}});
      await act(async()=>{await vi.advanceTimersByTimeAsync(10000);});
      expect(within(card).getByRole("switch")).toBeChecked();
      expect(within(card).getByLabelText("newTarget")).toHaveValue("My edited wordbook");
      expect(within(card).getByText("pending: 4")).toBeInTheDocument();
      expect(api.saveCloudConnection).not.toHaveBeenCalled();
    } finally {view.unmount();vi.useRealTimers();}
  });

  it("refreshes a clean form when another tab changes persisted settings",async()=>{
    vi.useFakeTimers();
    vi.mocked(api.listCloudConnections).mockResolvedValueOnce([{...connection,enabled:true}]).mockResolvedValue([{...connection,target_id:"other",target_name:"Other"}]);
    const view=render(<CloudVocabularyConnections/>);
    try {
      await act(async()=>{});
      fireEvent.click(screen.getByRole("button",{name:"eudic manage"}));
      const card=screen.getByRole("region",{name:"eudic"});
      expect(within(card).getByRole("switch")).toBeChecked();
      await act(async()=>{await vi.advanceTimersByTimeAsync(10000);});
      expect(within(card).getByRole("switch")).not.toBeChecked();
      expect(within(card).getByText("Other")).toBeInTheDocument();
      fireEvent.click(within(card).getByRole("button",{name:"save"}));
      await act(async()=>{});
      expect(api.saveCloudConnection).toHaveBeenCalledWith("eudic",{enabled:false,target_id:"other",target_name:"Other",add_to_study:false});
    } finally {view.unmount();vi.useRealTimers();}
  });
  it("blocks stale dirty drafts until the user discards and reloads remote settings",async()=>{
    vi.useFakeTimers();
    vi.mocked(api.listCloudConnections).mockResolvedValueOnce([{...connection,enabled:true}]).mockResolvedValue([{...connection,target_id:"other",target_name:"Other"}]);
    const view=render(<CloudVocabularyConnections/>);
    try {
      await act(async()=>{});
      fireEvent.click(screen.getByRole("button",{name:"eudic manage"}));
      const card=screen.getByRole("region",{name:"eudic"});
      fireEvent.change(within(card).getByLabelText("newTarget"),{target:{value:"Unsaved name"}});
      await act(async()=>{await vi.advanceTimersByTimeAsync(10000);});
      expect(within(card).getByText("configConflict")).toBeInTheDocument();
      expect(within(card).getByRole("button",{name:"save"})).toBeDisabled();
      expect(within(card).getByLabelText("newTarget")).toHaveValue("Unsaved name");
      expect(api.saveCloudConnection).not.toHaveBeenCalled();
      fireEvent.click(within(card).getByRole("button",{name:"reloadSettings"}));
      expect(within(card).getByRole("switch")).not.toBeChecked();
      expect(within(card).getByText("Other")).toBeInTheDocument();
      expect(within(card).getByRole("button",{name:"save"})).toBeEnabled();
      expect(within(card).queryByText("configConflict")).not.toBeInTheDocument();
    } finally {view.unmount();vi.useRealTimers();}
  });

});
