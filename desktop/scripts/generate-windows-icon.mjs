#!/usr/bin/env node
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";

const [source, output] = process.argv.slice(2);
if (!source || !output) {
  console.error("usage: generate-windows-icon.mjs <source.icns> <output.ico>");
  process.exit(2);
}

const pngSignature = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
const icns = readFileSync(source);
if (icns.toString("ascii", 0, 4) !== "icns") {
  throw new Error(`not an ICNS file: ${source}`);
}

const imagesBySize = new Map();
for (let offset = 8; offset + 8 <= icns.length;) {
  const length = icns.readUInt32BE(offset + 4);
  if (length < 8 || offset + length > icns.length) {
    throw new Error(`invalid ICNS chunk at offset ${offset}`);
  }
  const payload = icns.subarray(offset + 8, offset + length);
  const pngOffset = payload.indexOf(pngSignature);
  if (pngOffset >= 0) {
    const png = payload.subarray(pngOffset);
    const width = png.readUInt32BE(16);
    const height = png.readUInt32BE(20);
    if (width === height && [32, 64, 128, 256].includes(width) && !imagesBySize.has(width)) {
      imagesBySize.set(width, Buffer.from(png));
    }
  }
  offset += length;
}

const images = [...imagesBySize.entries()].sort(([left], [right]) => left - right);
if (images.length !== 4) {
  throw new Error(`expected ICNS PNG frames 32,64,128,256; found ${images.map(([size]) => size).join(",")}`);
}

const headerSize = 6 + images.length * 16;
const header = Buffer.alloc(headerSize);
header.writeUInt16LE(0, 0);
header.writeUInt16LE(1, 2);
header.writeUInt16LE(images.length, 4);
let imageOffset = headerSize;
images.forEach(([size, image], index) => {
  const entry = 6 + index * 16;
  header.writeUInt8(size === 256 ? 0 : size, entry);
  header.writeUInt8(size === 256 ? 0 : size, entry + 1);
  header.writeUInt8(0, entry + 2);
  header.writeUInt8(0, entry + 3);
  header.writeUInt16LE(1, entry + 4);
  header.writeUInt16LE(32, entry + 6);
  header.writeUInt32LE(image.length, entry + 8);
  header.writeUInt32LE(imageOffset, entry + 12);
  imageOffset += image.length;
});

mkdirSync(path.dirname(output), { recursive: true });
writeFileSync(output, Buffer.concat([header, ...images.map(([, image]) => image)]));
