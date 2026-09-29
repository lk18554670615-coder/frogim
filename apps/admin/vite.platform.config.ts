import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Separate output and entry: enterprise administrators never inherit this realm.
export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist-platform', rollupOptions: { input: 'platform.html' } },
  server: {
    host: '127.0.0.1', port: 4177,
    proxy: { '/platform/admin': { target: 'http://127.0.0.1:8090' } },
  },
});
