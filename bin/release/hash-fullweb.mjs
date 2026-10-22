import { createHash } from 'node:crypto';
import { readdirSync, readFileSync, writeFileSync, lstatSync } from 'node:fs';
import path from 'node:path';

// The manifest covers every file in the exact dist directory copied into Go embed.
export function enumerateFullWeb(root) {
  const files = [];
  function walk(directory, prefix = '') {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const relative = prefix ? prefix + '/' + entry.name : entry.name;
      const absolute = path.join(directory, entry.name);
      if (entry.isDirectory()) walk(absolute, relative);
      else {
        if (!entry.isFile() || lstatSync(absolute).isSymbolicLink()) {
          throw new Error('Full Web contains non-regular asset: ' + relative);
        }
        files.push({
          path: relative,
          sha256: 'sha256:' + createHash('sha256').update(readFileSync(absolute)).digest('hex'),
        });
      }
    }
  }
  walk(root);
  files.sort((a, b) => a.path.localeCompare(b.path, 'en'));
  if (!files.some((item) => item.path === 'index.html') || files.length < 2) {
    throw new Error('Full Web dist is missing index.html or assets');
  }
  return { schemaVersion: 1, files };
}

if (process.argv[1] && import.meta.url === new URL('file://' + process.argv[1]).href) {
  const [, , root, output] = process.argv;
  if (!root || !output) throw new Error('Usage: node hash-fullweb.mjs DIST OUT');
  writeFileSync(output, JSON.stringify(enumerateFullWeb(root), null, 2) + '\n');
}
