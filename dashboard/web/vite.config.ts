import { rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';

const apiTarget = 'http://127.0.0.1:8090';
const outDir = fileURLToPath(new URL('./dist', import.meta.url));

// Vite's emptyOutDir would also delete the committed dist/.gitkeep that keeps
// `go build` working without a frontend build, so only the build output is cleared.
function cleanBuildOutput(): Plugin {
  return {
    name: 'clean-build-output',
    apply: 'build',
    buildStart() {
      rmSync(`${outDir}/assets`, { recursive: true, force: true });
      rmSync(`${outDir}/index.html`, { force: true });
    },
  };
}

export default defineConfig({
  plugins: [react(), cleanBuildOutput()],
  base: '/',
  build: {
    outDir,
    emptyOutDir: false,
    assetsDir: 'assets',
    sourcemap: false,
    chunkSizeWarningLimit: 700,
  },
  server: {
    proxy: {
      '/api': apiTarget,
      '/healthz': apiTarget,
    },
  },
});
