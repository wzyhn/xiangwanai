import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {execFileSync} from 'node:child_process';
const root=path.resolve(import.meta.dirname,'..');
const read=p=>fs.readFileSync(path.join(root,p),'utf8');
test('registered SQL is complete and unchanged',()=>{
 const {files}=JSON.parse(read('deploy/sql/manifest.json'));
 const names=fs.readdirSync(path.join(root,'deploy/sql')).filter(f=>f.endsWith('.sql')).sort();
 assert.deepEqual(names,Object.keys(files).sort());
 for(const name of names) assert.equal(crypto.createHash('sha256').update(fs.readFileSync(path.join(root,'deploy/sql',name))).digest('hex'),files[name],name);
});
test('standalone workspace contains only Xiangwan applications',()=>{
 const apps=fs.readdirSync(path.join(root,'apps')).sort();
 assert.deepEqual(apps,['miniprogram-xiangwan','xiangwan-admin-web']);
 assert.match(read('pnpm-workspace.yaml'),/apps\/miniprogram-xiangwan/);
 assert.doesNotMatch(read('pnpm-workspace.yaml'),/apps\/\*/);
});
test('tracked distribution contains no sensitive paths or private keys',()=>{
 const files=execFileSync('git',['ls-files','-z'],{cwd:root,encoding:'utf8'}).split('\0').filter(Boolean);
 for(const file of files){
  assert.doesNotMatch(file,/(^|\/)(node_modules|miniprogram_npm|\.next|\.codex[^/]*|\.env(?!\.example$))(\/|$)|\.(pem|pfx|p12|key|exe|dll)$/i,file);
  if(/\.(go|js|mjs|ts|tsx|json|md|yml|yaml)$/.test(file)) assert.doesNotMatch(read(file),/-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/,file);
 }
});
