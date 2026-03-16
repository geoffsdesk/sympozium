import { defineConfig } from 'astro/config';
import react from '@astrojs/react';
import tailwind from '@astrojs/tailwind';
import mdx from '@astrojs/mdx';

export default defineConfig({
  integrations: [react(), tailwind(), mdx()],
  site: 'https://geoffsdesk.com',
  base: '/sympozium',
  output: 'static',
});
