import { ReactQueryDevtoolsPanel } from '@tanstack/react-query-devtools'

export default {
  name: 'Tanstack Query',
  // import.meta.env.DEV is automatically set by Vite based on the build mode
  // In production builds (vite build), this will be false and devtools won't be included
  render: () => (import.meta.env.DEV ? <ReactQueryDevtoolsPanel /> : <></>),
}
