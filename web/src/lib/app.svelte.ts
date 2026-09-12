import { api, ApiError } from './api';
import type { Session } from './types';

/** Toast notifications, shown bottom-right and dismissed automatically. */
export interface Toast {
	id: number;
	kind: 'success' | 'error' | 'info';
	message: string;
}

let nextToastID = 1;

class AppState {
	session = $state<Session | null>(null);
	sessionError = $state('');
	loading = $state(true);
	toasts = $state<Toast[]>([]);
	theme = $state<'dark' | 'light'>('dark');

	async loadSession() {
		this.loading = true;
		try {
			this.session = await api.session();
			this.sessionError = '';
		} catch (e) {
			this.session = null;
			this.sessionError =
				e instanceof ApiError && e.status === 403
					? 'This panel only answers requests coming through the WireGuard tunnel, and no peer is registered for your address.'
					: describe(e);
		} finally {
			this.loading = false;
		}
	}

	notify(kind: Toast['kind'], message: string) {
		const toast: Toast = { id: nextToastID++, kind, message };
		this.toasts = [...this.toasts, toast];
		setTimeout(() => this.dismiss(toast.id), kind === 'error' ? 7000 : 3500);
	}

	success(message: string) {
		this.notify('success', message);
	}

	/** Reports a failed action, unwrapping the API's error message. */
	fail(e: unknown, fallback = 'Something went wrong') {
		this.notify('error', describe(e, fallback));
	}

	dismiss(id: number) {
		this.toasts = this.toasts.filter((t) => t.id !== id);
	}

	setTheme(theme: 'dark' | 'light') {
		this.theme = theme;
		document.documentElement.dataset.theme = theme;
		try {
			localStorage.setItem('wgui:theme', theme);
		} catch {
			/* storage may be unavailable; the choice just will not persist */
		}
	}

	loadTheme() {
		let saved: string | null = null;
		try {
			saved = localStorage.getItem('wgui:theme');
		} catch {
			/* ignore */
		}
		this.setTheme(saved === 'light' ? 'light' : 'dark');
	}
}

export function describe(e: unknown, fallback = 'Something went wrong'): string {
	if (e instanceof ApiError) return e.message || fallback;
	if (e instanceof Error) return e.message || fallback;
	return fallback;
}

export const app = new AppState();

/**
 * Runs a callback immediately and then on an interval, pausing the repeats while
 * the tab is hidden so a forgotten tab does not keep asking for data.
 *
 * The first call always happens, hidden or not: a tab restored from a previous
 * session, or opened in the background, would otherwise render an empty table
 * that looks like an error until someone focuses it.
 */
export function poll(fn: () => void | Promise<void>, ms: number): () => void {
	let timer: ReturnType<typeof setInterval> | undefined;
	let running = false;

	const tick = async (force = false) => {
		if (running) return;
		if (!force && document.hidden) return;
		running = true;
		try {
			await fn();
		} finally {
			running = false;
		}
	};

	const stop = () => {
		if (timer) clearInterval(timer);
		timer = undefined;
	};
	const onVisibility = () => {
		if (!document.hidden) void tick();
	};

	void tick(true);
	stop();
	timer = setInterval(() => void tick(), ms);
	document.addEventListener('visibilitychange', onVisibility);

	return () => {
		stop();
		document.removeEventListener('visibilitychange', onVisibility);
	};
}
