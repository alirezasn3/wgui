export type Role = 'admin' | 'distributor' | 'user';
export type Status = 'active' | 'disabled' | 'expired' | 'quota';

export interface Peer {
	id: string;
	name: string;
	role: Role;
	ownerId: string;
	ownerName: string;
	groupId: number;
	groupName: string;
	publicKey: string;
	privateKey?: string;
	allowedIps: string;
	/** Overrides which server address this peer's generated config points at. */
	clientEndpoint: string;
	/** Pins the remote address the server sends this peer's traffic to. */
	preferredEndpoint: string;
	/** Bytes; 0 means unlimited. */
	allowedUsage: number;
	/** Unix ms; 0 means never expires. */
	expiresAt: number;
	totalTx: number;
	totalRx: number;
	manuallyDisabled: boolean;
	lastHandshakeAt: number;
	lastEndpoint: string;
	note: string;
	createdAt: number;
	updatedAt: number;

	usage: number;
	status: Status;
	groupAllowedUsage: number;
	groupExpiresAt: number;
	groupUsage: number;
	/** How many other peers this one has been granted sight of. */
	sharedCount: number;
	/** The server that saw this peer most recently, blank when it is this one. */
	seenOn: string;
	seenOnId: string;
	/** When this server last saw the peer; lastHandshakeAt is when any server did. */
	localHandshakeAt: number;

	/** Live values, read from the interface and never stored. */
	txSpeed: number;
	rxSpeed: number;
	online: boolean;
}

export interface Group {
	id: number;
	name: string;
	ownerId: string;
	ownerName: string;
	allowedUsage: number;
	expiresAt: number;
	manuallyDisabled: boolean;
	note: string;
	createdAt: number;
	updatedAt: number;
	peerCount: number;
	totalTx: number;
	totalRx: number;
	usage: number;
	status: Status;
}

/** Another wgui this one knows about, or this one itself. */
export interface Node {
	id: string;
	/** 'local' for this server, 'node' for one that syncs to it, 'master' for the one it follows. */
	role: 'local' | 'node' | 'master' | string;
	name: string;
	version: string;
	address: string;
	peerCount: number;
	onlinePeers: number;
	txSpeed: number;
	rxSpeed: number;
	recent: UsageWindows;
	firstSeenAt: number;
	lastSeenAt: number;
	online: boolean;
}

/** What a server carried over the recent past, in bytes. */
export interface UsageWindows {
	hour: number;
	day: number;
	week: number;
	month: number;
}

/** One server's view of a peer. */
export interface PeerPresence {
	serverId: string;
	serverName: string;
	role: string;
	isLocal: boolean;
	tx: number;
	rx: number;
	usage: number;
	lastHandshakeAt: number;
	lastEndpoint: string;
	online: boolean;
}

export interface PeerDefaults {
	allowedUsageBytes: number;
	expiryDays: number;
	role: Role;
	dns: string;
	allowedIPs: string;
	mtu: number;
	persistentKeepalive: number;
}

export interface Settings {
	publicAddress: string;
	endpoints: string[];
	defaultEndpoint: string;
	peerDefaults: PeerDefaults;
	groupDefaults: { allowedUsageBytes: number; expiryDays: number };
	qr: { color: string; caption: string; showName: boolean; showAddress: boolean };
	ipinfo: {
		enabled: boolean;
		baseURL: string;
		cacheTTLHours: number;
		apiKey: string;
		apiKeyHeader: string;
	};
	usageFlushSeconds: number;
}

export interface Session {
	peerId: string;
	name: string;
	role: Role;
	canWrite: boolean;
	/** False on a node for anything its master decides. */
	isAdmin: boolean;
	/** True when this server takes its peers from a master. */
	isNode: boolean;
	masterUrl: string;
	endpoints: string[];
	sortFields: string[];
	serverPublicKey: string;
	version: string;
	peerDefaults: PeerDefaults;
	qr: { color: string; caption: string; showName: boolean; showAddress: boolean };
}

export interface StatusCounts {
	total: number;
	active: number;
	disabled: number;
	expired: number;
	quota: number;
	/** Connected to this server. */
	online: number;
	/** Connected to any server in the fleet. */
	onlineAnywhere: number;
}

export interface ServerStatus {
	interface: string;
	listenPort: number;
	publicKey: string;
	peers: StatusCounts;
	live: { txSpeed: number; rxSpeed: number; online: number };
	usage: number;
	groups: number;
	recent: UsageWindows;
}

export interface IPInfo {
	ip: string;
	asn: number;
	asName: string;
	org: string;
	country: string;
	countryCode: string;
	cidr: string;
	fetchedAt: number;
	cached: boolean;
}

export interface Page<T> {
	total: number;
	page: number;
	pageSize: number;
	items: T[];
}

export type PeerBulkAction =
	| 'enable'
	| 'disable'
	| 'resetUsage'
	| 'setUsage'
	| 'setExpiry'
	| 'setGroup'
	| 'setRole'
	| 'delete';

/** Kernel settings the panel can read and change on the server it runs on. */
export interface SystemStatus {
	supported: boolean; // false off Linux
	writable: boolean; // false when wgui is not running as root

	ipForwardingV4: boolean;
	ipForwardingV6: boolean;

	congestionControl: string;
	availableAlgos: string[];
	defaultQdisc: string;
	bbrAvailable: boolean;
	bbrEnabled: boolean;
}

/** One read-only command's output, as an operator would see it on the server. */
export interface RuleSet {
	name: string;
	command: string;
	output: string;
	error: string;
	available: boolean;
	truncated: boolean;
}

export interface ScriptRun {
	id: number;
	scriptId: number;
	startedAt: number;
	endedAt: number;
	exitCode: number;
	output: string;
	error: string;
	/** What set it off: "manual" or "monitor". */
	source: string;
	actor: string;
}

export interface Script {
	id: number;
	name: string;
	description: string;
	body: string;
	timeoutSec: number;
	createdAt: number;
	updatedAt: number;
	lastRun?: ScriptRun;
	running: boolean;
}

export interface Monitor {
	id: number;
	name: string;
	/** A host or address for ICMP, or host:port for a TCP connection check. */
	target: string;
	intervalSec: number;
	timeoutSec: number;
	failuresBefore: number;
	scriptId: number;
	scriptName: string;
	rearmOnRecovery: boolean;
	enabled: boolean;
	consecutiveFailures: number;
	lastCheckedAt: number;
	lastOkAt: number;
	lastRttMs: number;
	lastError: string;
	firedAt: number;
	createdAt: number;
	updatedAt: number;
}
