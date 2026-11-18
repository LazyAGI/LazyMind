import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import type { DesktopUpdateState } from "@/runtime/desktopUpdates";
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, options?: {count?:number}) => options?.count === undefined ? key : `${key}:${options.count}` }) }));
import DesktopUpdateSettings, { DesktopUpdateNotice } from "./DesktopUpdateSettings";
let listener: (state: DesktopUpdateState) => void;
const install=vi.fn(), later=vi.fn(), download=vi.fn(), reveal=vi.fn();
const base: DesktopUpdateState={revision:1,phase:"available",currentVersion:"1.0.0",nextVersion:"1.1.0",releaseId:"fixture-r2",canInstall:true};
let current: DesktopUpdateState;
beforeEach(()=>{
  vi.clearAllMocks();current={...base};
  Object.assign(window,{lazymindDesktop:{updateStatus:async()=>current,updateInstall:install,updateLater:later,updateDownload:download,updateReveal:reveal,onUpdateState:(fn:typeof listener)=>{listener=fn;return()=>{};}}});
  install.mockResolvedValue({...base,revision:2,phase:"downloading",updating:true});
  later.mockResolvedValue({...base,revision:9,phase:"downloaded",updating:false,packageAvailable:true});
});
afterEach(()=>{cleanup();Reflect.deleteProperty(window,"lazymindDesktop");});
it("offers one update and restart action and shows that downloading is not installation",async()=>{
  render(<DesktopUpdateSettings/>);fireEvent.click(await screen.findByRole("button",{name:"desktopUpdates.install"}));
  await waitFor(()=>expect(install).toHaveBeenCalledWith("fixture-r2"));expect(download).not.toHaveBeenCalled();
  expect(await screen.findByRole("button",{name:"desktopUpdates.later"})).toBeEnabled();expect(screen.queryByText("desktopUpdates.installed")).not.toBeInTheDocument();
});
it("keeps waiting and countdown visible globally with a working later action",async()=>{
  current={...base,phase:"waiting",updating:true};render(<MemoryRouter><DesktopUpdateNotice/></MemoryRouter>);
  expect(await screen.findByText("desktopUpdates.waiting")).toBeInTheDocument();
  act(()=>listener({...current,revision:3,phase:"countdown",secondsRemaining:10}));expect(screen.getByText("desktopUpdates.countdown:10")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button",{name:"desktopUpdates.later"}));await waitFor(()=>expect(later).toHaveBeenCalledOnce());
  expect(await screen.findByText("desktopUpdates.noticeDownloaded")).toBeInTheDocument();
});
it("reopening requires explicit continue and never updates on mount",async()=>{
  current={...base,phase:"interrupted",pendingUpdate:true};render(<DesktopUpdateSettings/>);
  expect(await screen.findByText("desktopUpdates.interruptedUpdate")).toBeInTheDocument();expect(install).not.toHaveBeenCalled();
  expect(screen.getByRole("button",{name:"desktopUpdates.check"})).toBeDisabled();fireEvent.click(screen.getByRole("button",{name:"desktopUpdates.continueUpdate"}));
  await waitFor(()=>expect(install).toHaveBeenCalledWith("fixture-r2"));
});
it("uncertain installation exposes the retained package and repair instructions without retrying install",async()=>{
  current={...base,phase:"repair",packageAvailable:true,reason:"update_incomplete"};render(<DesktopUpdateSettings/>);
  expect(await screen.findByText("desktopUpdates.repair")).toBeInTheDocument();expect(screen.getByRole("alert")).toHaveTextContent("desktopUpdates.reasons.update_incomplete");
  expect(screen.queryByRole("button",{name:"desktopUpdates.install"})).not.toBeInTheDocument();expect(screen.queryByRole("button",{name:"desktopUpdates.continueUpdate"})).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button",{name:"desktopUpdates.reveal"}));await waitFor(()=>expect(reveal).toHaveBeenCalledOnce());
});
it("a confirmed new version is the only successful completion notice",async()=>{
  current={...base,phase:"installing",updating:true};render(<MemoryRouter><DesktopUpdateNotice/></MemoryRouter>);
  expect(await screen.findByText("desktopUpdates.installing")).toBeInTheDocument();expect(screen.queryByRole("button",{name:"desktopUpdates.later"})).not.toBeInTheDocument();
  act(()=>listener({...base,revision:4,phase:"installed",currentVersion:"1.1.0"}));expect(screen.getByText("desktopUpdates.installed")).toBeInTheDocument();
});
it("component incompatibility explains the block and permits checking without offering installation",async()=>{
  current={revision:3,phase:"blocked",currentVersion:"1.0.0",canInstall:true,reason:"component_update_incompatible",pendingUpdate:false,pendingDownload:false};
  render(<DesktopUpdateSettings/>);
  expect(await screen.findByRole("alert")).toHaveTextContent("desktopUpdates.reasons.component_update_incompatible");
  expect(screen.getByRole("button",{name:"desktopUpdates.check"})).toBeEnabled();
  expect(screen.queryByRole("button",{name:"desktopUpdates.install"})).not.toBeInTheDocument();
  expect(screen.queryByRole("button",{name:"desktopUpdates.continueUpdate"})).not.toBeInTheDocument();
  expect(screen.queryByText("desktopUpdates.repair")).not.toBeInTheDocument();
});
