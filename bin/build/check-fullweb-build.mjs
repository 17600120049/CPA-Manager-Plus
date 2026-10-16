import assert from 'node:assert/strict';
import { readFileSync, readdirSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = fileURLToPath(new URL('../../', import.meta.url));
const dist = path.join(root, 'apps/web/dist/fullweb');
const read = (name) => readFileSync(path.join(dist, name), 'utf8');
const manifest = JSON.parse(read('.vite/manifest.json'));
const graph = JSON.parse(read('.vite/chunk-graph.json'));
const entry = Object.values(manifest).find((item) => item.isEntry);
assert(entry, 'Full Web entry missing');
assert(
  Object.values(manifest).some((item) => item.isDynamicEntry),
  'lazy route missing'
);
const initial = new Set();
function walk(file) {
  if (initial.has(file)) return;
  initial.add(file);
  for (const dependency of graph[file].imports) walk(dependency);
}
walk(entry.file);
const initialModules = [...initial].flatMap((file) => graph[file].modules);
assert(
  !initialModules.some((id) => /ConsoleCarrier|\/features\/|codemirror|\/yaml\/|echarts/i.test(id)),
  'Bootstrap eagerly imports Console/heavy code'
);
assert(
  Object.values(graph).some((chunk) =>
    chunk.modules.some((id) => id.includes('ConsoleCarrier.tsx'))
  ),
  'Console seam not in same build'
);
const emitted = readdirSync(path.join(dist, 'assets'));
assert(emitted.filter((name) => name.endsWith('.js')).length >= 2, 'JS not split');
assert(emitted.filter((name) => name.endsWith('.css')).length >= 2, 'CSS not split');
assert(
  emitted.some((name) => name.endsWith('.svg')),
  'static asset not split'
);
assert(read('index.html').includes('./assets/'), 'HTML must reference external assets');
assert(!read('index.html').includes('data:image'), 'Full Web assets were inlined');
for (const item of Object.values(manifest)) {
  for (const file of [item.file, ...(item.css || []), ...(item.assets || [])])
    assert(existsSync(path.join(dist, file)), `missing dist resource ${file}`);
}
const panelDir = path.join(root, 'apps/web/dist/panel');
const panel = readFileSync(path.join(panelDir, 'management.html'), 'utf8');
assert.deepEqual(
  readdirSync(panelDir),
  ['management.html'],
  'Panel Lite must be one HTML artifact'
);
assert(
  !/(?:src|href)=["'](?:\.\/)?assets\//.test(panel),
  'Panel references runtime sibling resources'
);
assert(panel.includes('<script') && panel.includes('<style'), 'Panel JS/CSS missing');
assert(
  !panel.includes('/usage-service/updates') && !panel.includes('/usage-service/runtime/updates'),
  'Panel retained Manager update transport'
);
console.log(
  JSON.stringify(
    {
      fullWebEntry: entry.file,
      lazyRoutes: Object.values(manifest)
        .filter((item) => item.isDynamicEntry)
        .map((item) => item.file),
      assets: emitted,
      initialModuleCount: initialModules.length,
      panel: 'single-file management.html',
    },
    null,
    2
  )
);
