// Explicit source selection for Electron 31 (which has no cross-platform native picker).
function installScreenCapture({ session, desktopCapturer, Menu, getWindow, onSelected = () => {}, log = () => {} }) {
  session.setDisplayMediaRequestHandler(async (request, callback) => {
    const window = getWindow();
    let replied = false;
    const reply = (streams) => {
      if (replied) return;
      replied = true;
      try { callback(streams); } catch { /* Requesting frame may have navigated away. */ }
    };
    log("request", { mainFrame: Boolean(window && !window.isDestroyed() && request.frame === window.webContents.mainFrame), userGesture: request.userGesture, videoRequested: request.videoRequested, audioRequested: request.audioRequested });
    if (!window || window.isDestroyed() || request.frame !== window.webContents.mainFrame ||
        !request.userGesture || !request.videoRequested || request.audioRequested) {
      log("request-rejected");
      reply({});
      return;
    }
    try {
      const sources = await desktopCapturer.getSources({ types: ["screen"], thumbnailSize: { width: 0, height: 0 } });
      log("sources", { count: sources.length });
      if (window.isDestroyed() || request.frame !== window.webContents.mainFrame || !sources.length) {
        reply({}); return;
      }
      const menu = Menu.buildFromTemplate(sources.map((source) => ({
        label: source.name.replace(/&/g, "&&"),
        click: () => {
          if (window.isDestroyed() || request.frame !== window.webContents.mainFrame) { reply({}); return; }
          log("source-selected");
          onSelected(source); reply({ video: source });
        },
      })));
      menu.popup({ window, callback: () => { log("picker-closed", { selected: replied }); reply({}); } });
    } catch (error) { log("capture-error", { name: error?.name, message: error instanceof Error ? error.message : String(error) }); reply({}); }
  });
}
module.exports = { installScreenCapture };
