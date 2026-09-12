import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		// The panel is a single-page app served by the Go binary: every route is
		// resolved in the browser, so the build only needs one fallback document.
		adapter: adapter({ fallback: '200.html' })
	}
};

export default config;
