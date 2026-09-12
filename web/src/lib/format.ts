/** Formats a byte count with binary units, e.g. "1.44 GiB". */
export function bytes(value: number, digits = 2): string {
	if (!value || value < 0) return '0 B';
	const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
	let i = 0;
	let n = value;
	while (n >= 1024 && i < units.length - 1) {
		n /= 1024;
		i++;
	}
	return `${n.toFixed(i === 0 ? 0 : digits)} ${units[i]}`;
}

/**
 * Formats a transfer rate, e.g. "2.30 MiB/s". Only a genuinely idle peer reads
 * as a dash: a keepalive's worth of traffic is still traffic worth seeing.
 */
export function speed(bytesPerSecond: number): string {
	if (!bytesPerSecond || bytesPerSecond < 0) return '—';
	return `${bytes(bytesPerSecond, 1)}/s`;
}

export const GIB = 1024 ** 3;

/** Converts gibibytes from a form field into bytes; 0 stays unlimited. */
export function gibToBytes(gib: number): number {
	if (!gib || gib <= 0) return 0;
	return Math.round(gib * GIB);
}

export function bytesToGib(value: number): number {
	if (!value) return 0;
	return Math.round((value / GIB) * 100) / 100;
}

/** A quota or expiry of 0 means "no limit" throughout the panel. */
export function isUnlimited(value: number): boolean {
	return !value;
}

export function quota(value: number): string {
	return isUnlimited(value) ? '∞' : bytes(value);
}

/**
 * Describes how far away a timestamp is, e.g. "in 12 days" or "3 hours ago".
 * Returns "never" for the 0 that means "does not expire".
 */
export function relative(timestamp: number): string {
	if (!timestamp) return 'never';

	const diff = timestamp - Date.now();
	const past = diff < 0;
	const seconds = Math.abs(diff) / 1000;

	// "0 seconds ago" reads worse than saying it plainly.
	if (seconds < 10) return past ? 'just now' : 'in a moment';

	const units: [number, string][] = [
		[60, 'second'],
		[60, 'minute'],
		[24, 'hour'],
		[30, 'day'],
		[12, 'month']
	];

	let n = seconds;
	let unit = 'second';
	for (const [step, name] of units) {
		if (n < step) break;
		n /= step;
		unit = nextUnit(name);
	}

	const rounded = Math.floor(n);
	const label = `${rounded} ${unit}${rounded === 1 ? '' : 's'}`;
	return past ? `${label} ago` : `in ${label}`;
}

function nextUnit(current: string): string {
	switch (current) {
		case 'second':
			return 'minute';
		case 'minute':
			return 'hour';
		case 'hour':
			return 'day';
		case 'day':
			return 'month';
		default:
			return 'year';
	}
}

/** Short absolute date for tooltips and detail panels. */
export function absolute(timestamp: number): string {
	if (!timestamp) return 'never';
	return new Date(timestamp).toLocaleString(undefined, {
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit'
	});
}

export function handshake(timestamp: number): string {
	return timestamp ? relative(timestamp) : 'never connected';
}

/** Percentage of an allowance consumed; unlimited allowances read as 0. */
export function usagePercent(used: number, allowed: number): number {
	if (!allowed) return 0;
	return Math.min(100, Math.round((used / allowed) * 100));
}

export function statusLabel(status: string): string {
	switch (status) {
		case 'active':
			return 'Active';
		case 'disabled':
			return 'Disabled';
		case 'expired':
			return 'Expired';
		case 'quota':
			return 'Depleted';
		default:
			return status;
	}
}

/** What a peer is actually limited to, and by whom. */
export interface EffectiveLimits {
	/** What this peer itself has consumed. */
	used: number;
	/** The allowance itself; 0 means unlimited. */
	allowed: number;
	/**
	 * Everything consumed against that allowance when it is shared with a group
	 * — this peer's share included. 0 when the allowance is the peer's alone.
	 */
	shared: number;
	/** Expiry that actually applies; 0 means never. */
	expiresAt: number;
	/** Set when the allowance comes from the group rather than the peer. */
	usageFromGroup: string;
	/** Set when the expiry comes from the group rather than the peer. */
	expiryFromGroup: string;
}

interface LimitedPeer {
	groupId: number;
	usage: number;
	allowedUsage: number;
	expiresAt: number;
	groupName: string;
	groupUsage: number;
	groupAllowedUsage: number;
	groupExpiresAt: number;
}

/**
 * Works out which limits actually bind a peer.
 *
 * Belonging to a group hands the group the whole say over data and time: its
 * allowance and expiry replace the member's own outright rather than competing
 * with them, so a generous group does not stay capped by a member's old quota
 * and an expired member is not cut off inside a live group. The member's own
 * figures are untouched underneath and apply again the moment it leaves.
 *
 * The usage shown is still the peer's own — what this one device has spent out
 * of the shared pot — with the group's running total alongside it so the
 * remaining figure can tell the truth about the pot.
 */
export function effectiveLimits(p: LimitedPeer): EffectiveLimits {
	if (p.groupId) {
		return {
			used: p.usage,
			allowed: p.groupAllowedUsage,
			shared: p.groupUsage,
			expiresAt: p.groupExpiresAt,
			usageFromGroup: p.groupName,
			expiryFromGroup: p.groupName
		};
	}
	return {
		used: p.usage,
		allowed: p.allowedUsage,
		shared: 0,
		expiresAt: p.expiresAt,
		usageFromGroup: '',
		expiryFromGroup: ''
	};
}
