import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync, spawnSync } from 'node:child_process';
const checker = path.join(import.meta.dirname, 'check-public-boundary.mjs');

test('public boundary rejects staged credentials, keys and symlinks while permitting an example', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'xiangwan-public-boundary-'));
  const git = (...args) => execFileSync('git', args, { cwd: root, stdio: 'pipe' });
  const check = () => spawnSync(process.execPath, [checker], { cwd: root, encoding: 'utf8' });
  try {
    git('init', '--quiet');
    fs.writeFileSync(path.join(root, 'mcp.example.json'), '{"token":"REPLACE_LOCALLY"}\n');
    git('add', 'mcp.example.json');
    assert.equal(check().status, 0);
    fs.writeFileSync(path.join(root, 'mcp.json'), '{"token":"synthetic-test"}\n');
    git('add', 'mcp.json');
    assert.equal(check().status, 1);
    git('rm', '--cached', 'mcp.json');
    fs.writeFileSync(path.join(root, 'innocent.txt'), ['-----BEGIN ', 'PRIVATE KEY-----\nsynthetic\n'].join(''));
    git('add', 'innocent.txt');
    assert.equal(check().status, 1);
    git('rm', '--cached', 'innocent.txt');
    const oid = git('hash-object', '-w', '--stdin').toString().trim();
    git('update-index', '--add', '--cacheinfo', `120000,${oid},link`);
    assert.equal(check().status, 1);
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});
