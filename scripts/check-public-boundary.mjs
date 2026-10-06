import { execFileSync } from 'node:child_process';

// Use the Git index, not an ignored local configuration or generated output.
const entries = execFileSync('git', ['ls-files', '--stage', '-z'], { encoding: 'utf8' }).split('\0').filter(Boolean);
const objects = [...new Set(entries.map(entry => entry.split('\t')[0].split(' ')[1]))];
let batch;
try {
  batch = execFileSync('git', ['cat-file', '--batch'], { input: objects.join('\n') + '\n', maxBuffer: 128 * 1024 * 1024 });
} catch { console.error('Cannot read staged objects within the public audit size limit.'); process.exit(1); }
const bodies = new Map();
let cursor = 0;
for (const oid of objects) {
  const end = batch.indexOf(10, cursor);
  const [returnedOid, type, size] = batch.subarray(cursor, end).toString('ascii').split(' ');
  if (returnedOid !== oid || type !== 'blob' || !/^\d+$/.test(size)) {
    console.error('Public index contains an unsupported object.'); process.exit(1);
  }
  cursor = end + 1;
  bodies.set(oid, batch.subarray(cursor, cursor + Number(size)));
  cursor += Number(size) + 1;
}
let failures = 0;
for (const entry of entries) {
  const [meta, path] = entry.split('\t');
  const [mode, oid] = meta.split(' ');
  const forbidden = /(^|\/)(?:\.git|node_modules|miniprogram_npm|\.next|\.codex[^/]*|secrets)(?:\/|$)|(^|\/)(?:\.?mcp\.json|AppSecret\.txt|credentials\.json|project\.private\.config\.json|id_rsa[^/]*|id_ed25519[^/]*)$|(^|\/)\.env(?:$|\.(?!example$))|\.(?:pem|key|p12|pfx|p8|jks|keystore|zip|7z|dump|backup|sqlite\w*|exe|dll|log)$|\.secret\.|\.local\.json$/i;
  const reasons = [];
  if (forbidden.test(path)) reasons.push('sensitive/generated path');
  if (!['100644', '100755'].includes(mode)) reasons.push('symlink or submodule');
  const bytes = bodies.get(oid);
  if (/-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/.test(bytes.toString('utf8'))) reasons.push('private key material');
  if (reasons.length) { console.error(`${path}: ${reasons.join(', ')}`); failures++; }
}
if (failures) process.exitCode = 1;
else console.log(`Public boundary: ${entries.length} tracked files checked.`);
