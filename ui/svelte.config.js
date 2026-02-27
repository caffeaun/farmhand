import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		adapter: adapter({
			pages: 'dist',
			assets: 'dist',
			fallback: 'index.html',
			precompress: false,
			strict: false
		}),
		paths: {
			base: ''
		},
		prerender: {
			handleHttpError: ({ path, referrer, message }) => {
				// Ignore 404s for static assets
				if (path.endsWith('.png') || path.endsWith('.ico') || path.endsWith('.svg')) {
					return;
				}
				throw new Error(message);
			},
			handleUnseenRoutes: () => {
				// Dynamic routes like /jobs/[id] are rendered client-side via the SPA fallback
			}
		}
	}
};

export default config;
