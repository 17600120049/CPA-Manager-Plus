import { cpSync, existsSync, mkdirSync, readdirSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = fileURLToPath(new URL('../../', import.meta.url));
const dist = path.join(root, 'apps/web/dist/fullweb');
const target = path.join(root, 'apps/manager-server/internal/httpapi/web/fullweb');
if (!existsSync(path.join(dist, 'index.html'))) throw new Error('Build Full Web first');
mkdirSync(target, { recursive: true });
// This directory contains generated build output only (apart from .gitkeep).
for (const name of readdirSync(target)) {
  if (name !== '.gitkeep') rmSync(path.join(target, name), { recursive: true, force: true });
}
cpSync(dist, target, { recursive: true });
