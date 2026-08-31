#!/usr/bin/env node
/**
 * Minimal HTTP wrapper around html_to_pptx.mjs for the ppt-export compose service.
 *
 * POST /export  JSON: { "deck_dir": "/data/subagent/.../ppt_decks/<id>" }
 * GET  /health
 *
 * Returns the last JSON status line from html_to_pptx.mjs.
 */
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const PORT = Number(process.env.PORT || 8099);
const CLI = resolve(__dirname, 'html_to_pptx.mjs');

function readJson(req) {
  return new Promise((resolveBody, reject) => {
    const chunks = [];
    req.on('data', (c) => chunks.push(c));
    req.on('end', () => {
      try {
        const raw = Buffer.concat(chunks).toString('utf8') || '{}';
        resolveBody(JSON.parse(raw));
      } catch (e) {
        reject(e);
      }
    });
    req.on('error', reject);
  });
}

function runExport(deckDir) {
  return new Promise((resolveResult) => {
    const child = spawn(process.execPath, [CLI, '--deck-dir', deckDir, '--force'], {
      cwd: __dirname,
      env: process.env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => { stdout += d.toString(); });
    child.stderr.on('data', (d) => { stderr += d.toString(); });
    child.on('close', (code) => {
      const lines = (stdout + '\n' + stderr).split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
      let payload = null;
      for (let i = lines.length - 1; i >= 0; i--) {
        const line = lines[i];
        if (line.startsWith('{') && line.endsWith('}')) {
          try {
            payload = JSON.parse(line);
            break;
          } catch {
            // continue
          }
        }
      }
      if (!payload) {
        payload = {
          status: code === 0 ? 'ok' : 'failed',
          error: (stderr || stdout || `exit ${code}`).slice(0, 2000),
        };
      }
      payload._returncode = code;
      resolveResult(payload);
    });
  });
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url || '/', `http://${req.headers.host || 'localhost'}`);
  if (req.method === 'GET' && url.pathname === '/health') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ status: 'ok', cli: existsSync(CLI) }));
    return;
  }
  if (req.method === 'POST' && url.pathname === '/export') {
    try {
      const body = await readJson(req);
      const deckDir = String(body.deck_dir || '').trim();
      if (!deckDir) {
        res.writeHead(400, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ status: 'failed', error: 'deck_dir required' }));
        return;
      }
      if (!existsSync(deckDir)) {
        res.writeHead(404, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ status: 'failed', error: `deck_dir not found: ${deckDir}` }));
        return;
      }
      const payload = await runExport(deckDir);
      // html_to_pptx.mjs emits {success:true,...}; normalize for callers.
      if (payload.success === true && !payload.status) {
        payload.status = 'ok';
      }
      if (payload.success === false || (Number(payload.failed) > 0 && Number(payload.converted || 0) < 1)) {
        payload.status = 'failed';
        payload.success = false;
      }
      if (payload.status === 'ok' && payload.success === false) {
        payload.status = 'failed';
      }
      // Prefer process exit code when JSON claimed success but conversion failed.
      if (Number(payload._returncode) !== 0) {
        payload.status = 'failed';
        payload.success = false;
      }
      const ok = payload.status === 'ok' || payload.success === true;
      res.writeHead(ok ? 200 : 500, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(payload));
    } catch (e) {
      res.writeHead(500, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ status: 'failed', error: String(e && e.message ? e.message : e) }));
    }
    return;
  }
  res.writeHead(404, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ status: 'failed', error: 'not found' }));
});

server.listen(PORT, '0.0.0.0', () => {
  console.error(`[ppt-export] listening on :${PORT}`);
});
