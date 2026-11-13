import { mkdtemp, writeFile, rm, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { build } from 'vite';
import { it } from 'node:test';
import assert from 'node:assert/strict';

it('ships the reader’s Chinese character maps in production builds', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'lazymind-pdf-'));
  try {
    const input = path.join(dir, 'entry.js');
    await writeFile(input, 'globalThis.pdfFixture = true;');
    const result = await build({
      configFile: path.resolve('vite.config.ts'),
      logLevel: 'silent',
      build: { write: false, minify: false, rollupOptions: { input } },
    });
    const outputs = (Array.isArray(result) ? result : [result]).flatMap(result => 'output' in result ? result.output : []);
    const cmap = outputs.find(item => item.fileName === 'pdfjs/cmaps/GBK-EUC-H.bcmap');
    assert.ok(cmap, 'Chinese PDFs require a locally served GBK CMap');
    const require = createRequire(import.meta.url);
    const pdfjsRoot = path.dirname(require.resolve('pdfjs-dist/package.json', { paths: [path.dirname(require.resolve('react-pdf/package.json'))] }));
    assert.deepEqual(Buffer.from(cmap.source), await readFile(path.join(pdfjsRoot, 'cmaps/GBK-EUC-H.bcmap')));
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

// A synthetic PDF containing 北京 in GBK; no personal attachment is committed.
function chinesePdf() {
  const stream = 'BT /F1 24 Tf 20 50 Td <b1b1bea9> Tj ET';
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << /Font << /F1 4 0 R >> >> /Contents 6 0 R >>',
    '<< /Type /Font /Subtype /Type0 /BaseFont /STSong-Light /Encoding /GBK-EUC-H /DescendantFonts [5 0 R] >>',
    '<< /Type /Font /Subtype /CIDFontType0 /BaseFont /STSong-Light /FontDescriptor 7 0 R /CIDSystemInfo << /Registry (Adobe) /Ordering (GB1) /Supplement 4 >> >>',
    `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`,
    '<< /Type /FontDescriptor /FontName /STSong-Light /Flags 6 /FontBBox [0 -200 1000 900] /ItalicAngle 0 /Ascent 900 /Descent -200 /CapHeight 700 /StemV 80 >>',
  ];
  let pdf = '%PDF-1.4\n';
  const offsets = [0];
  objects.forEach((object, index) => {
    offsets.push(pdf.length);
    pdf += `${index + 1} 0 obj\n${object}\nendobj\n`;
  });
  const xref = pdf.length;
  pdf += `xref\n0 8\n0000000000 65535 f \n${offsets.slice(1).map(offset => `${String(offset).padStart(10, '0')} 00000 n \n`).join('')}trailer\n<< /Size 8 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF`;
  return new TextEncoder().encode(pdf);
}

for (const base of ['/', '/reader/']) {
  it(`decodes Chinese text using application options and development assets at ${base}`, async () => {
    const { createServer } = await import('vite');
    const { pathToFileURL } = await import('node:url');
    const require = createRequire(import.meta.url);
    const entry = require.resolve('pdfjs-dist/legacy/build/pdf.mjs', { paths: [path.dirname(require.resolve('react-pdf/package.json'))] });
    const { getDocument } = await import(pathToFileURL(entry).href);
    const server = await createServer({
      configFile: path.resolve('vite.config.ts'), base, logLevel: 'silent',
      server: { host: '127.0.0.1', port: 0, open: false },
      optimizeDeps: { noDiscovery: true, include: [], disabled: true },
    });
    let task;
    try {
      await server.listen();
      const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
      const { pdfDocumentOptions } = await server.ssrLoadModule('/src/components/ui/pdfDocumentOptions.ts');
      task = getDocument({ ...pdfDocumentOptions, cMapUrl: new URL(pdfDocumentOptions.cMapUrl, origin).href, data: chinesePdf(), useWorkerFetch: true });
      const document = await task.promise;
      const text = await (await document.getPage(1)).getTextContent();
      assert.equal(text.items.map(item => item.str || '').join(''), '北京');
      const license = await fetch(`${origin}${base}pdfjs/cmaps/LICENSE`);
      assert.equal(license.status, 200);
      assert.match(await license.text(), /Copyright/);
    } finally {
      await task?.destroy();
      await server.close();
    }
  });
}
