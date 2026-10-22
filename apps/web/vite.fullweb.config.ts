import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'node:path';

function loadingBoundaryEvidence(): Plugin {
  return {
    name: 'fullweb-loading-boundary',
    generateBundle(_options, bundle) {
      const graph = Object.fromEntries(
        Object.values(bundle)
          .filter((item) => item.type === 'chunk')
          .map((chunk) => [
            chunk.fileName,
            {
              entry: chunk.isEntry,
              imports: chunk.imports,
              dynamicImports: chunk.dynamicImports,
              modules: Object.keys(chunk.modules).map((id) =>
                id
                  .replaceAll('\\', '/')
                  .replace(path.resolve(__dirname, '../..').replaceAll('\\', '/') + '/', '')
              ),
            },
          ])
      );
      this.emitFile({
        type: 'asset',
        fileName: '.vite/chunk-graph.json',
        source: JSON.stringify(graph, null, 2),
      });
    },
  };
}

// Full Web is a separate browser entry. Panel's single-file settings never
// participate in this build. Relative chunks resolve against their module URL;
// Manager binds the HTML asset references to the verified canonical prefix.
export default defineConfig({
  root: path.resolve(__dirname, 'fullweb'),
  publicDir: false,
  base: './',
  define: {
    __PANEL_LITE__: 'false',
    __DEMO_SITE__: 'false',
    __APP_VERSION__: JSON.stringify(process.env.VERSION || 'dev'),
  },
  plugins: [react(), loadingBoundaryEvidence()],
  resolve: { alias: { '@': path.resolve(__dirname, 'src') } },
  css: {
    modules: {
      localsConvention: 'camelCase',
      generateScopedName: '[name]__[local]___[hash:base64:5]',
    },
    preprocessorOptions: {
      scss: {
        additionalData: `@use "@/styles/variables" as *;\n@use "@/styles/mixins" as *;\n`,
      },
    },
  },
  build: {
    target: 'es2020',
    outDir: path.resolve(__dirname, 'dist/fullweb'),
    emptyOutDir: true,
    manifest: true,
    assetsInlineLimit: 0,
    cssCodeSplit: true,
  },
});
