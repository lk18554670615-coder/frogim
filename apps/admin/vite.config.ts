import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '');

  return {
    base: env.ENTERPRISE_ADMIN_BASE || '/',
    plugins: [react()],
    server: {
      port: 4173,
      proxy: {
        '/api': {
          target: env.ADMIN_PROXY_TARGET ?? 'http://localhost:8080',
          changeOrigin: true,
        },
      },
    },
    test: {
      environment: 'jsdom',
      setupFiles: './src/test-setup.ts',
      css: true,
      globals: true,
    },
  };
});
