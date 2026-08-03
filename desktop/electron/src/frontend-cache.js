async function clearFrontendCaches(session, log = () => {}) {
  if (!session) {
    return;
  }

  await session.clearCache();
  await session.clearStorageData({
    storages: ["serviceworkers", "cachestorage"],
  });
  log("cleared frontend HTTP, Service Worker, and Cache Storage caches");
}

module.exports = { clearFrontendCaches };
