<script lang="ts">
	import { api } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import { absolute, bytes, effectiveLimits, handshake, quota, relative, speed } from '$lib/format';
	import {
		canShareFiles,
		configFileName,
		download,
		nameWasShortened,
		qrPng,
		shareFiles,
		tunnelName
	} from '$lib/qr';
	import type { IPInfo, Peer, PeerPresence } from '$lib/types';
	import CopyButton from './CopyButton.svelte';
	import Icon from './Icon.svelte';
	import Modal from './Modal.svelte';
	import QrCode from './QrCode.svelte';
	import StatusTag from './StatusTag.svelte';
	import UsageBar from './UsageBar.svelte';
	import VisibilityEditor from './VisibilityEditor.svelte';

	interface Props {
		open: boolean;
		peer: Peer | null;
		canModify: boolean;
		onclose: () => void;
		onedit: (peer: Peer) => void;
		/** Asks the page to open this peer's group. */
		ongroup: (groupId: number) => void;
		onchanged: () => void;
	}
	let { open, peer, canModify, onclose, onedit, ongroup, onchanged }: Props = $props();

	let config = $state('');
	let configError = $state('');
	let ipInfo = $state<IPInfo | null>(null);
	let ipError = $state('');
	let lookingUp = $state(false);
	let sharing = $state(false);

	// Where else this peer has been seen. Loaded on open and refreshed slowly:
	// another server's view only changes when it next syncs, so polling it at
	// the rate of the local figures would ask far more often than it can move.
	let presence = $state<PeerPresence[]>([]);
	let presenceFor = $state<string | null>(null);

	// Load the configuration whenever a different peer is opened. The private key
	// is only in this response, so it is fetched on demand rather than listed.
	// The peer object is replaced on every poll, so this effect must act on the
	// identity rather than the object: re-running it each tick would refetch the
	// configuration and, worse, throw away an endpoint lookup the operator had
	// just asked for.
	let loadedFor = $state<string | null>(null);

	$effect(() => {
		const id = open ? (peer?.id ?? null) : null;
		if (id === loadedFor) return;
		loadedFor = id;

		if (!id) {
			config = '';
			return;
		}
		ipInfo = null;
		ipError = '';
		configError = '';
		api
			.peerConfig(id)
			.then((text) => (config = text))
			.catch((e) => {
				config = '';
				configError = e instanceof Error ? e.message : String(e);
			});
	});

	async function lookup() {
		if (!peer) return;
		lookingUp = true;
		ipError = '';
		try {
			ipInfo = await api.peerIPInfo(peer.id);
		} catch (e) {
			ipError = e instanceof Error ? e.message : String(e);
		} finally {
			lookingUp = false;
		}
	}

	// WireGuard names the tunnel after the file, and that name has to fit a
	// network interface: 15 characters from a restricted set. A longer peer name
	// is shortened here rather than producing a config the client refuses.
	let fileName = $derived(peer ? configFileName(peer.name) : '');
	let shortened = $derived(!!peer && nameWasShortened(peer.name));

	let qrOptions = $derived.by(() => {
		const qr = app.session?.qr;
		return {
			data: config,
			size: 288,
			color: qr?.color || '#023020',
			topText: qr?.showName === false ? undefined : peer?.name,
			bottomText: qr?.showAddress === false ? undefined : peer?.allowedIps,
			caption: qr?.caption || undefined
		};
	});

	function configFile(): File {
		return new File([config], fileName, { type: shareType });
	}

	async function qrFile(): Promise<File> {
		// Rendered larger than the on-screen copy so a shared image stays sharp.
		const blob = await qrPng({ ...qrOptions, size: 640, scale: 2 });
		return new File([blob], `${tunnelName(peer!.name)}.png`, { type: 'image/png' });
	}

	function downloadConfig() {
		if (!config) return;
		// Deliberately opaque rather than text/plain: browsers — Safari in
		// particular — "correct" a text/plain download to match its type and hand
		// the operator wg0.conf.txt, which WireGuard will not import.
		download(new Blob([config], { type: 'application/octet-stream' }), fileName);
	}

	async function downloadQR() {
		try {
			const file = await qrFile();
			download(file, file.name);
		} catch (e) {
			app.fail(e, 'Could not save the QR code');
		}
	}

	async function shareConfig() {
		try {
			await shareFiles([configFile()], peer?.name ?? 'WireGuard configuration');
		} catch (e) {
			app.fail(e, 'Could not share the configuration');
		}
	}

	async function shareQR() {
		try {
			const file = await qrFile();
			await shareFiles([file], peer?.name ?? 'WireGuard configuration');
		} catch (e) {
			app.fail(e, 'Could not share the QR code');
		}
	}

	// Desktop browsers often cannot share files at all, so the buttons only
	// appear where they would work.
	// An opaque type is what keeps the .conf extension — a file shared as
	// text/plain arrives as .conf.txt, which WireGuard will not import — but not
	// every browser will share one, so fall back to the type they all accept.
	let shareType = $derived(
		canShareFiles([new File([''], 'a.conf', { type: 'application/octet-stream' })])
			? 'application/octet-stream'
			: 'text/plain'
	);
	let canShare = $derived(canShareFiles([new File([''], 'a.conf', { type: shareType })]));

	$effect(() => {
		const id = open ? (peer?.id ?? null) : null;
		if (id === presenceFor) return;
		presenceFor = id;
		presence = [];
		if (!id) return;
		void loadPresence(id);
	});

	async function loadPresence(id: string) {
		try {
			const res = await api.peerPresence(id);
			if (presenceFor === id) presence = res.presence;
		} catch {
			/* the section simply stays empty */
		}
	}

	// Only worth showing once more than one server has carried the peer.
	let elsewhere = $derived(presence.filter((p) => !p.isLocal));

	async function resetUsage() {
		if (!peer) return;
		try {
			await api.updatePeer(peer.id, { resetUsage: true });
			app.success(`Reset usage for ${peer.name}`);
			onchanged();
		} catch (e) {
			app.fail(e);
		}
	}

	let limits = $derived(
		peer
			? effectiveLimits(peer)
			: { used: 0, allowed: 0, shared: 0, expiresAt: 0, usageFromGroup: '', expiryFromGroup: '' }
	);
</script>

<Modal {open} title={peer?.name ?? ''} subtitle={peer?.allowedIps} width="46rem" {onclose}>
	{#if peer}
		<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_288px]">
			<div class="min-w-0 space-y-5">
				<div class="flex flex-wrap items-center gap-2">
					<StatusTag status={peer.status} online={peer.online} />
					{#if peer.groupName}
						<button
							class="tag cursor-pointer hover:underline"
							style="background: var(--surface-3); color: var(--text-muted)"
							onclick={() => ongroup(peer.groupId)}
							title="Open {peer.groupName}"
						>
							{peer.groupName}
						</button>
					{/if}
					<span
						class="tag capitalize"
						style="background: var(--surface-3); color: var(--text-muted)"
					>
						{peer.role}
					</span>
					{#if peer.ownerName}
						<span class="tag" style="background: var(--surface-3); color: var(--text-muted)">
							owned by {peer.ownerName}
						</span>
					{/if}
				</div>

				<dl class="grid grid-cols-1 gap-x-4 gap-y-3 text-sm sm:grid-cols-2">
					<div>
						<dt class="label mb-0.5">Data used</dt>
						<dd>
							<UsageBar
								used={limits.used}
								allowed={limits.allowed}
								shared={limits.shared}
								source={limits.usageFromGroup || undefined}
							/>
						</dd>
					</div>
					<div>
						<dt class="label mb-0.5">Expires</dt>
						<dd>
							{relative(limits.expiresAt)}
							{#if limits.expiresAt}
								<div class="text-[11px]" style="color: var(--text-faint)">
									{absolute(limits.expiresAt)}
								</div>
							{/if}
							{#if limits.expiryFromGroup}
								<div class="text-[11px]" style="color: var(--warning)">
									via {limits.expiryFromGroup}
								</div>
							{/if}
						</dd>
					</div>
					<div>
						<dt class="label mb-0.5">Last handshake</dt>
						<dd>{handshake(peer.lastHandshakeAt)}</dd>
					</div>
					<div>
						<dt class="label mb-0.5">Live rate</dt>
						<dd class="flex font-mono text-xs tabular-nums">
							<span class="w-[7.5rem] shrink-0" style="color: var(--success)">
								↓ {speed(peer.txSpeed)}
							</span>
							<span class="w-[7.5rem] shrink-0" style="color: var(--info)">
								↑ {speed(peer.rxSpeed)}
							</span>
						</dd>
					</div>
					<div>
						<dt class="label mb-0.5">Transferred</dt>
						<dd class="font-mono text-xs tabular-nums" style="color: var(--text-muted)">
							<span class="whitespace-nowrap">↓ {bytes(peer.totalTx)}</span>
							<span class="whitespace-nowrap">· ↑ {bytes(peer.totalRx)}</span>
						</dd>
					</div>
					<div>
						<dt class="label mb-0.5">Allowance</dt>
						<dd class="font-mono text-xs" style="color: var(--text-muted)">
							{quota(limits.allowed)}
							{#if limits.usageFromGroup}
								<span class="block text-[11px]" style="color: var(--text-faint)">
									shared with {limits.usageFromGroup}
								</span>
							{/if}
						</dd>
					</div>
				</dl>

				{#if elsewhere.length > 0}
					<div>
						<div class="label mb-1.5">Seen across the fleet</div>
						<ul class="space-y-2">
							{#each presence as row (row.serverId)}
								<li class="flex items-start gap-2 text-xs">
									<span
										class="mt-1.5 inline-block h-1.5 w-1.5 shrink-0 rounded-full"
										style="background: {row.online ? 'var(--success)' : 'var(--text-faint)'}"
									></span>
									<span class="min-w-0 flex-1">
										<span class="flex flex-wrap items-baseline gap-x-2">
											<span class="truncate font-medium">{row.serverName}</span>
											{#if row.isLocal}
												<span class="text-[10px]" style="color: var(--text-faint)">here</span>
											{/if}
											<span class="tabular-nums" style="color: var(--text-muted)">
												{handshake(row.lastHandshakeAt)}
											</span>
										</span>
										<span
											class="block truncate font-mono text-[11px]"
											style="color: var(--text-faint)"
										>
											{row.lastEndpoint || 'never connected'}
										</span>
									</span>
									<span class="shrink-0 font-mono tabular-nums" style="color: var(--text-muted)">
										{bytes(row.usage)}
									</span>
								</li>
							{/each}
						</ul>
						<p class="mt-1.5 text-[11px]" style="color: var(--text-faint)">
							One allowance, wherever it is spent. Another server's line is as fresh as its last
							sync.
						</p>
					</div>
				{/if}

				<!-- Endpoint and who it belongs to -->
				<div>
					<div class="label">Current endpoint</div>
					{#if peer.lastEndpoint}
						<div class="flex flex-wrap items-center gap-2">
							<span class="font-mono text-xs">{peer.lastEndpoint}</span>
							<button class="btn btn-default btn-sm" onclick={lookup} disabled={lookingUp}>
								<Icon name="globe" size={13} />
								{lookingUp ? 'Looking up…' : 'Look up ISP'}
							</button>
						</div>
						{#if ipInfo}
							<dl
								class="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 rounded-md px-3 py-2 text-xs"
								style="background: var(--surface-2)"
							>
								<dt style="color: var(--text-muted)">ISP</dt>
								<dd>{ipInfo.asName || '—'}</dd>
								<dt style="color: var(--text-muted)">Organisation</dt>
								<dd>{ipInfo.org || '—'}</dd>
								<dt style="color: var(--text-muted)">ASN</dt>
								<dd class="font-mono">{ipInfo.asn ? `AS${ipInfo.asn}` : '—'}</dd>
								{#if ipInfo.cidr}
									<dt style="color: var(--text-muted)">Network</dt>
									<dd class="font-mono">{ipInfo.cidr}</dd>
								{/if}
								{#if ipInfo.country || ipInfo.countryCode}
									<dt style="color: var(--text-muted)">Country</dt>
									<dd>{ipInfo.country || ipInfo.countryCode}</dd>
								{/if}
							</dl>
							{#if ipInfo.cached}
								<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
									From the cache, looked up {relative(ipInfo.fetchedAt)}
								</p>
							{/if}
						{/if}
						{#if ipError}
							<p class="mt-1 text-xs" style="color: var(--danger)">{ipError}</p>
						{/if}
					{:else}
						<p class="text-xs" style="color: var(--text-faint)">Never connected</p>
					{/if}
				</div>

				{#if peer.preferredEndpoint}
					<div>
						<div class="label">Pinned remote endpoint</div>
						<span class="font-mono text-xs">{peer.preferredEndpoint}</span>
					</div>
				{/if}

				{#if peer.note}
					<div>
						<div class="label">Note</div>
						<p class="text-sm whitespace-pre-wrap" style="color: var(--text-muted)">{peer.note}</p>
					</div>
				{/if}

				{#if canModify && peer.role === 'user'}
					<div>
						<button class="btn btn-default btn-sm" onclick={() => (sharing = true)}>
							<Icon name="share" size={13} /> Manage what this user can see
							{#if peer.sharedCount}<span style="color: var(--text-faint)"
									>({peer.sharedCount})</span
								>{/if}
						</button>
					</div>
				{/if}
			</div>

			<!-- Configuration and QR -->
			<div class="flex w-full min-w-0 flex-col items-center gap-3">
				{#if configError}
					<p class="text-xs" style="color: var(--text-faint)">{configError}</p>
				{:else if config}
					<QrCode
						value={config}
						size={288}
						color={qrOptions.color}
						topText={qrOptions.topText}
						bottomText={qrOptions.bottomText}
						caption={qrOptions.caption}
					/>

					<div class="w-full space-y-2">
						<div class="flex gap-2">
							<button class="btn btn-default btn-sm flex-1" onclick={downloadQR}>
								<Icon name="download" size={13} /> QR image
							</button>
							{#if canShare}
								<button class="btn btn-default btn-sm flex-1" onclick={shareQR}>
									<Icon name="share" size={13} /> Share QR
								</button>
							{/if}
						</div>
						<div class="flex gap-2">
							<button class="btn btn-primary btn-sm flex-1" onclick={downloadConfig}>
								<Icon name="download" size={13} />
								{fileName}
							</button>
							{#if canShare}
								<button class="btn btn-default btn-sm flex-1" onclick={shareConfig}>
									<Icon name="share" size={13} /> Share
								</button>
							{/if}
						</div>
						<CopyButton
							value={config}
							label="Copy configuration"
							class="btn btn-default btn-sm w-full"
						/>
					</div>

					{#if shortened}
						<p class="text-center text-[11px]" style="color: var(--text-faint)">
							Saved as <span class="font-mono">{fileName}</span> — WireGuard tunnel names are limited
							to 15 characters.
						</p>
					{/if}

					<details class="w-full">
						<summary class="cursor-pointer text-xs" style="color: var(--text-muted)">
							Show configuration
						</summary>
						<!-- Wrapped rather than scrolled: a key is one long unbroken
						     token, and a horizontal scrollbar inside a dialog is worse
						     than a wrapped line. -->
						<pre
							class="mt-2 max-h-48 w-full overflow-y-auto rounded-md p-2.5 font-mono text-[10px] leading-relaxed break-all whitespace-pre-wrap"
							style="background: var(--surface-2)">{config}</pre>
					</details>
				{/if}
			</div>
		</div>
	{/if}

	{#snippet footer()}
		{#if canModify && peer}
			<button class="btn btn-default" onclick={resetUsage}>
				<Icon name="refresh" size={14} /> Reset usage
			</button>
			<button class="btn btn-primary" onclick={() => onedit(peer)}>
				<Icon name="edit" size={14} /> Edit
			</button>
		{:else}
			<button class="btn btn-default" onclick={onclose}>Close</button>
		{/if}
	{/snippet}
</Modal>

{#if peer}
	<VisibilityEditor
		open={sharing}
		{peer}
		onclose={() => (sharing = false)}
		onsaved={() => {
			sharing = false;
			onchanged();
		}}
	/>
{/if}
