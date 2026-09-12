import type {
	Group,
	IPInfo,
	Peer,
	PeerBulkAction,
	ServerStatus,
	Session,
	Monitor,
	Node,
	PeerPresence,
	RuleSet,
	Script,
	ScriptRun,
	Settings,
	SystemStatus
} from './types';

export class ApiError extends Error {
	constructor(
		message: string,
		readonly status: number
	) {
		super(message);
		this.name = 'ApiError';
	}
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
	let res: Response;
	try {
		res = await fetch(`/api${path}`, {
			...init,
			headers: {
				...(init?.body ? { 'content-type': 'application/json' } : {}),
				...init?.headers
			}
		});
	} catch {
		throw new ApiError('Cannot reach the server', 0);
	}

	if (!res.ok) {
		// Error bodies are {"error": "..."}, but a proxy or crash can return
		// something else, so fall back to the status text.
		let message = res.statusText;
		try {
			const body = await res.json();
			if (body?.error) message = body.error;
		} catch {
			/* not JSON */
		}
		throw new ApiError(message, res.status);
	}

	if (res.status === 204) return undefined as T;
	const text = await res.text();
	if (!text) return undefined as T;

	const type = res.headers.get('content-type') ?? '';
	return (type.includes('json') ? JSON.parse(text) : text) as T;
}

function query(params: Record<string, string | number | boolean | undefined>): string {
	const search = new URLSearchParams();
	for (const [key, value] of Object.entries(params)) {
		if (value === undefined || value === '' || value === false) continue;
		search.set(key, String(value));
	}
	const s = search.toString();
	return s ? `?${s}` : '';
}

export interface PeerQuery {
	search?: string;
	status?: string;
	group?: number;
	role?: string;
	owner?: string;
	sort?: string;
	order?: 'asc' | 'desc';
	page?: number;
	pageSize?: number;
}

export interface PeerListResult {
	peers: Peer[];
	total: number;
	page: number;
	pageSize: number;
}

export interface GroupListResult {
	groups: Group[];
	total: number;
	page: number;
	pageSize: number;
}

/** Fields accepted when creating or editing a peer. */
export interface PeerInput {
	name?: string;
	role?: string;
	ownerId?: string;
	groupId?: number;
	allowedUsage?: number;
	expiresAt?: number;
	expiryDays?: number;
	clientEndpoint?: string;
	preferredEndpoint?: string;
	manuallyDisabled?: boolean;
	note?: string;
	resetUsage?: boolean;
}

export interface GroupInput {
	name?: string;
	ownerId?: string;
	allowedUsage?: number;
	expiresAt?: number;
	expiryDays?: number;
	manuallyDisabled?: boolean;
	note?: string;
	resetUsage?: boolean;
}

export interface PeerBulkInput {
	ids: string[];
	action: PeerBulkAction;
	allowedUsage?: number;
	expiresAt?: number;
	expiryDays?: number;
	groupId?: number;
	role?: string;
}

export const api = {
	session: () => request<Session>('/session'),
	status: () => request<ServerStatus>('/status'),

	system: () => request<SystemStatus>('/system'),
	networkRules: () => request<{ rules: RuleSet[] }>('/system/network'),
	enableIPForwarding: () => request<SystemStatus>('/system/ip-forwarding', { method: 'POST' }),
	setCongestion: (algorithm: string) =>
		request<SystemStatus>('/system/congestion', {
			method: 'POST',
			body: JSON.stringify({ algorithm })
		}),

	settings: () => request<Settings>('/settings'),
	saveSettings: (settings: Settings) =>
		request<Settings>('/settings', { method: 'PUT', body: JSON.stringify(settings) }),

	peers: (q: PeerQuery = {}) => request<PeerListResult>(`/peers${query({ ...q })}`),
	peer: (id: string) => request<Peer>(`/peers/${encodeURIComponent(id)}`),
	createPeer: (input: PeerInput) =>
		request<Peer>('/peers', { method: 'POST', body: JSON.stringify(input) }),
	updatePeer: (id: string, input: PeerInput) =>
		request<Peer>(`/peers/${encodeURIComponent(id)}`, {
			method: 'PATCH',
			body: JSON.stringify(input)
		}),
	deletePeer: (id: string) =>
		request<void>(`/peers/${encodeURIComponent(id)}`, { method: 'DELETE' }),
	bulkPeers: (input: PeerBulkInput) =>
		request<{ affected: number }>('/peers/bulk', { method: 'POST', body: JSON.stringify(input) }),
	peerConfig: (id: string) => request<string>(`/peers/${encodeURIComponent(id)}/config`),
	peerIPInfo: (id: string) => request<IPInfo>(`/peers/${encodeURIComponent(id)}/ipinfo`),
	visibility: (id: string) =>
		request<{ targets: string[] }>(`/peers/${encodeURIComponent(id)}/visibility`),
	setVisibility: (id: string, targets: string[]) =>
		request<void>(`/peers/${encodeURIComponent(id)}/visibility`, {
			method: 'PUT',
			body: JSON.stringify({ targets })
		}),

	scripts: () => request<{ scripts: Script[] }>('/scripts'),
	script: (id: number) => request<Script>(`/scripts/${id}`),
	createScript: (input: Partial<Script>) =>
		request<Script>('/scripts', { method: 'POST', body: JSON.stringify(input) }),
	updateScript: (id: number, input: Partial<Script>) =>
		request<Script>(`/scripts/${id}`, { method: 'PATCH', body: JSON.stringify(input) }),
	deleteScript: (id: number) => request<void>(`/scripts/${id}`, { method: 'DELETE' }),
	runScript: (id: number) => request<ScriptRun>(`/scripts/${id}/run`, { method: 'POST' }),
	stopScript: (id: number) => request<void>(`/scripts/${id}/stop`, { method: 'POST' }),
	scriptRuns: (id: number) => request<{ runs: ScriptRun[] }>(`/scripts/${id}/runs`),

	monitors: () => request<{ monitors: Monitor[] }>('/monitors'),
	createMonitor: (input: Partial<Monitor>) =>
		request<Monitor>('/monitors', { method: 'POST', body: JSON.stringify(input) }),
	updateMonitor: (id: number, input: Partial<Monitor> & { resetState?: boolean }) =>
		request<Monitor>(`/monitors/${id}`, { method: 'PATCH', body: JSON.stringify(input) }),
	deleteMonitor: (id: number) => request<void>(`/monitors/${id}`, { method: 'DELETE' }),
	checkMonitor: (id: number) =>
		request<{ ok: boolean; rttMs: number; error?: string }>(`/monitors/${id}/check`, {
			method: 'POST'
		}),

	groups: (
		q: { search?: string; status?: string; sort?: string; order?: string; pageSize?: number } = {}
	) => request<GroupListResult>(`/groups${query({ ...q })}`),
	group: (id: number) => request<Group>(`/groups/${id}`),

	nodes: () => request<{ nodes: Node[] }>('/nodes'),
	peerPresence: (id: string) =>
		request<{ presence: PeerPresence[] }>(`/peers/${encodeURIComponent(id)}/presence`),
	forgetNode: (id: string) =>
		request<void>(`/nodes/${encodeURIComponent(id)}`, { method: 'DELETE' }),
	/** Resets traffic counters: this server's with no ids, otherwise the servers named. */
	resetTraffic: (serverIds: string[] = []) =>
		request<void>('/traffic/reset', { method: 'POST', body: JSON.stringify({ serverIds }) }),
	syncSecret: () => request<{ secret: string; fingerprint: string }>('/nodes/secret'),
	rotateSyncSecret: () =>
		request<{ secret: string; fingerprint: string }>('/nodes/secret', { method: 'POST' }),
	createGroup: (input: GroupInput) =>
		request<Group>('/groups', { method: 'POST', body: JSON.stringify(input) }),
	updateGroup: (id: number, input: GroupInput) =>
		request<Group>(`/groups/${id}`, { method: 'PATCH', body: JSON.stringify(input) }),
	deleteGroup: (id: number) => request<void>(`/groups/${id}`, { method: 'DELETE' }),
	bulkGroups: (input: {
		ids: number[];
		action: string;
		allowedUsage?: number;
		expiryDays?: number;
	}) =>
		request<{ affected: number }>('/groups/bulk', { method: 'POST', body: JSON.stringify(input) })
};
