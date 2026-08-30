import { defineConfig, loadEnv } from 'vite'
import viteReact from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

import { tanstackRouter } from '@tanstack/router-plugin/vite'
import { resolve } from 'node:path'

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  // Load env file based on mode (development, production, etc.)
  const env = loadEnv(mode, process.cwd(), '')


  // Gateway URL - the gateway proxies to the license service with M2M authentication
  // In development, we use the local gateway which handles M2M auth
  const gatewayUrl = env.VITE_API_BASE_URL || 'http://localhost:8089'

  // License service URL for InstanceService (activation/refresh) - can go direct since these are public
  const licenseServiceUrl = env.VITE_LICENSE_SERVICE_URL || 'https://license.everstack.ai'
  // Auth service URL for instance connect flow (cloud auth service)
  const authServiceUrl = env.VITE_AUTH_SERVICE_URL || 'http://localhost:8093'
  const allowedHosts = env.VITE_ALLOWED_HOSTS
    ? env.VITE_ALLOWED_HOSTS.split(',').map((host) => host.trim()).filter(Boolean)
    : true
  const hmrHost = env.VITE_HMR_HOST || 'localhost'
  const hmrPort = parseInt(env.VITE_HMR_PORT || env.PORT || '3000')
  return {
    plugins: [
      tanstackRouter({ autoCodeSplitting: true }),
      viteReact(),
      tailwindcss(),
    ],
    // optimizeDeps: {
    //   include: ['monaco-yaml', 'monaco-editor'],
    // },
    resolve: {
      alias: {
        '@': resolve(__dirname, './src'),
      },
    },
    server: {
      port: parseInt(env.PORT || '3000'),
      strictPort: true,
      // Allow requests from the Go server proxy
      cors: true,
      // Dynamic tenant subdomains (e.g. *.127.0.0.1.sslip.io) should work in local dev
      // without manually updating this file. Set VITE_ALLOWED_HOSTS to tighten the list.
      allowedHosts,
      hmr: {
        host: hmrHost,
        port: hmrPort,
        clientPort: hmrPort,
        protocol: 'ws',
      },
      // Proxy configuration:
      // - LicenseService (spend limits, etc.) -> Gateway (which adds M2M auth and proxies to license service)
      // - InstanceService (activate/refresh) -> License service direct (these are public endpoints)
      proxy: {
        '/everstack.license.v1.LicenseService': {
          target: gatewayUrl,
          changeOrigin: true,
        },
        '/everstack.license.v1.InstanceService': {
          target: licenseServiceUrl,
          changeOrigin: true,
        },
        // Auth service (ConnectRPC) - instance connect flow
        '/everstack.auth.v1.AuthService': {
          target: authServiceUrl,
          changeOrigin: true,
        },
      },
      fs: {
        // Allow importing files from monorepo packages
        allow: [resolve(__dirname, '../../')],
      },
    },
    build: {
      chunkSizeWarningLimit: 1500,
      rollupOptions: {
        output: {
          manualChunks(id) {
            if (id.includes('node_modules')) {
              if (id.includes('monaco-editor')) return 'vendor-monaco'
              if (id.includes('@xyflow')) return 'vendor-reactflow'
              if (id.includes('react-dom') || id.includes('/react/') || id.includes('@radix-ui')) return 'vendor-react'
              if (id.includes('@bufbuild') || id.includes('@connectrpc')) return 'vendor-proto'
            }
          },
        },
      },
    },
  }
})
