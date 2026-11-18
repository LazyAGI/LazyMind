const fs = require("node:fs");
const path = require("node:path");
const { randomUUID } = require("node:crypto");

function valid(record) {
  return record && record.version === 1 && Object.keys(record).length === 4
    && typeof record.releaseId === "string" && /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(record.releaseId)
    && [record.sourceVersion, record.nextVersion].every(value => typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$/.test(value));
}

// A reminder only. It cannot authorize installation or supply a trusted path.
function createDownloadIntentStore(filename) {
  return {
    read() {
      let fd;
      try {
        fd = fs.openSync(filename, "r");
        const info = fs.fstatSync(fd);
        if (!info.isFile() || info.size > 4096) throw new Error("invalid_download_intent");
        const bytes = Buffer.alloc(4097);
        const length = fs.readSync(fd, bytes, 0, bytes.length, 0);
        if (length > 4096) throw new Error("invalid_download_intent");
        const record = JSON.parse(bytes.subarray(0, length).toString("utf8"));
        if (!valid(record)) throw new Error("invalid_download_intent");
        return record;
      } catch (error) {
        if (error.code === "ENOENT") return null;
        throw error;
      } finally { if (fd !== undefined) fs.closeSync(fd); }
    },
    write(record) {
      if (!valid(record)) throw new Error("invalid_download_intent");
      fs.mkdirSync(path.dirname(filename), { recursive: true, mode: 0o700 });
      const temporary = `${filename}.${randomUUID()}.tmp`;
      let fd;
      try {
        fd = fs.openSync(temporary, "wx", 0o600);
        fs.writeFileSync(fd, JSON.stringify(record)); fs.fsyncSync(fd);
        fs.closeSync(fd); fd = undefined;
        fs.renameSync(temporary, filename);
      } finally {
        if (fd !== undefined) fs.closeSync(fd);
        try { fs.unlinkSync(temporary); } catch (error) { if (error.code !== "ENOENT") throw error; }
      }
    },
    clear() {
      try { fs.unlinkSync(filename); } catch (error) { if (error.code !== "ENOENT") throw error; }
    },
  };
}
module.exports = { createDownloadIntentStore };
