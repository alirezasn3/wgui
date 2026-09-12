import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		// During development the API is served by the Go binary; everything under
		// /api is forwarded to it so the browser sees a single origin.
		proxy: {
			'/api': {
				target: process.env.WGUI_API ?? 'http://127.0.0.1:8080',
				changeOrigin: false,
				secure: false
			}
		}
	}
});
