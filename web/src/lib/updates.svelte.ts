import { api } from './api';
import type { UpdateStatus } from './types';

/** How long to wait for the server to come back as the new version. */
const restartTimeoutMs = 3 * 60 * 1000;

const dismissedKey = 'wgui.update.dismissed';

/**
 * What this server knows about newer releases, shared by the banner every page
 * shows and the Updates section in Settings.
 */
class Updates {
	status = $state<UpdateStatus | null>(null);
	/** Set while following an install through the server's restart. */
	installing = $state<{ version: string; startedAt: number; down: boolean } | null>(null);
	/** The server did not come back as the new version in time. */
	stalled = $state(false);
	dismissed = $state('');

	constructor() {
		try {
			this.dismissed = localStorage.getItem(dismissedKey) ?? '';
		} catch {
			/* storage unavailable: the banner simply shows again */
		}
	}

	/** The banner shows until the operator dismisses that particular version. */
	get showBanner() {
		const s = this.status;
		return !!s && s.available && s.latest !== this.dismissed && !this.installing;
	}

	dismiss() {
		if (!this.status) return;
		this.dismissed = this.status.latest;
		try {
			localStorage.setItem(dismissedKey, this.dismissed);
		} catch {
			/* remembered for this page only */
		}
	}

	async refresh() {
		try {
			this.status = await api.updateStatus();
		} catch {
			/* a server without updates, or one restarting: keep what we had */
		}
	}

	async check() {
		this.status = await api.checkForUpdate();
	}

	async install(version: string) {
		this.stalled = false;
		this.status = await api.installUpdate(version);
		this.installing = { version, startedAt: Date.now(), down: false };
		void this.follow();
	}

	/**
	 * Follows an install to its end: the job's progress while this server is
	 * still the old version, then its absence while it restarts, then the new
	 * version answering — at which point the page is reloaded, because the
	 * interface being shown is the old one.
	 */
	private async follow() {
		while (this.installing) {
			await new Promise((r) => setTimeout(r, 1000));
			const target = this.installing;
			if (!target) return;
			try {
				const s = await api.updateStatus();
				this.status = s;
				if (s.current === target.version) {
					location.reload();
					return;
				}
				if (s.job.state === 'failed') {
					this.installing = null;
					return;
				}
				this.installing = { ...target, down: false };
			} catch {
				// Not answering is the restart itself.
				this.installing = { ...target, down: true };
			}
			if (Date.now() - target.startedAt > restartTimeoutMs) {
				this.stalled = true;
				this.installing = null;
			}
		}
	}
}

export const updates = new Updates();
