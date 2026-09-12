<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { app, poll } from '$lib/app.svelte';
	import Confirm from '$lib/components/Confirm.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import NetworkRules from '$lib/components/NetworkRules.svelte';
	import StatCard from '$lib/components/StatCard.svelte';
	import StatusTag from '$lib/components/StatusTag.svelte';
	import { bytes, handshake, speed } from '$lib/format';
	import { relative } from '$lib/format';
	import type { Node, Peer, ServerStatus } from '$lib/types';
	import { onDestroy, onMount } from 'svelte';

	let status = $state<ServerStatus | null>(null);
	let busiest = $state<Peer[]>([]);
	let expiring = $state<Peer[]>([]);
	let fleet = $state<Node[]>([]);
	let failed = $state('');

	let isAdmin = $derived(app.session?.isAdmin ?? false);
	let isNode = $derived(app.session?.isNode ?? false);

	async function loadStatus() {
		try {
			const [s, top, soon] = await Promise.all([
				api.status(),
				api.peers({ sort: 'speed', order: 'desc', pageSize: 6 }),
				api.peers({ sort: 'expiry', order: 'asc', pageSize: 6, status: 'active' })
			]);
			status = s;
			busiest = top.peers;
			// The expiry sort puts "never expires" last, so anything without a
			// date is not worth listing here.
			expiring = soon.peers.filter((p) => p.expiresAt > 0);
			failed = '';
		} catch (e) {
			failed = e instanceof Error ? e.message : String(e);
		}
	}

	async function loadFleet() {
		try {
			fleet = (await api.nodes()).nodes;
		} catch {
			/* the section simply stays as it was */
		}
	}

	onMount(() => {
		// A plain user has no dashboard to look at; send it straight to its own
		// connection details.
		if (!app.session?.canWrite) {
			void goto('/peers');
			return;
		}

		// The fleet changes at the pace servers sync, not the pace the dashboard
		// polls, so it is fetched on its own slower schedule.
		if (app.session?.isAdmin) {
			onDestroyFleet = poll(loadFleet, 5000);
		}

		return poll(loadStatus, 1000);
	});

	let onDestroyFleet: (() => void) | undefined;
	onDestroy(() => onDestroyFleet?.());

	// Worth a section only once there is more than this server to show.
	let showFleet = $derived(fleet.length > 1);
	// A master can reset its nodes, which it reaches through their syncs; a node
	// can reach nothing but itself.
	let nodeRows = $derived(fleet.filter((n) => n.role === 'node'));

	const windows = [
		{ key: 'hour', label: 'Last hour' },
		{ key: 'day', label: 'Last 24 hours' },
		{ key: 'week', label: 'Last 7 days' },
		{ key: 'month', label: 'Last 30 days' }
	] as const;

	let confirming = $state<{
		title: string;
		message: string;
		serverIds: string[];
		done: string;
	} | null>(null);
	let confirmBusy = $state(false);

	const untouched = "Peers' usage and quotas are not affected.";

	function askResetHere() {
		confirming = {
			title: 'Reset traffic on this server?',
			message: `The last hour, day, week and month start again from zero. ${untouched}`,
			serverIds: [],
			done: 'Traffic counters reset'
		};
	}

	function askResetNode(node: Node) {
		const name = node.name || node.id.slice(0, 12);
		confirming = {
			title: `Reset traffic on ${name}?`,
			message: `Its counters start again from zero when it next syncs, usually within seconds. ${untouched}`,
			serverIds: [node.id],
			done: `${name} will reset at its next sync`
		};
	}

	function askResetAll() {
		const self = fleet.find((n) => n.role === 'local');
		confirming = {
			title: 'Reset traffic on every server?',
			message: `This server's counters start again from zero now, and each node's when it next syncs. ${untouched}`,
			serverIds: [...(self ? [self.id] : []), ...nodeRows.map((n) => n.id)],
			done: 'Traffic counters reset across the fleet'
		};
	}

	async function confirmReset() {
		if (!confirming) return;
		confirmBusy = true;
		try {
			await api.resetTraffic(confirming.serverIds);
			app.success(confirming.done);
			confirming = null;
			await Promise.all([loadStatus(), isAdmin ? loadFleet() : Promise.resolve()]);
		} catch (e) {
			app.fail(e);
		} finally {
			confirmBusy = false;
		}
	}
</script>

<div class="mb-5">
	<h1 class="text-xl font-semibold">Dashboard</h1>
	<p class="text-xs" style="color: var(--text-muted)">
		{#if status?.interface}
			Interface <span class="font-mono">{status.interface}</span> on port
			<span class="font-mono">{status.listenPort}</span>
		{:else}
			Waiting for the interface…
		{/if}
	</p>
</div>

{#if failed}
	<div class="card mb-4 p-3 text-sm" style="border-color: var(--danger); color: var(--danger)">
		{failed}
	</div>
{/if}

{#if status}
	<div class="mb-5 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
		<StatCard label="Peers" value={String(status.peers.total)} hint="registered" icon="peers" />
		<StatCard
			label="Online now"
			value={String(status.peers.online)}
			hint={showFleet && status.peers.onlineAnywhere > status.peers.online
				? `on this server, ${status.peers.onlineAnywhere} across the fleet`
				: 'handshaked in the last 3 minutes'}
			icon="check"
			accent="var(--success)"
		/>
		<StatCard
			label="Download"
			value={speed(status.live.txSpeed)}
			hint="live, to the peers"
			icon="down"
			accent="var(--info)"
		/>
		<StatCard
			label="Upload"
			value={speed(status.live.rxSpeed)}
			hint="live, from the peers"
			icon="up"
			accent="var(--warning)"
		/>
	</div>

	<section class="card mb-5 overflow-hidden">
		<header
			class="flex items-center justify-between border-b px-4 py-2.5"
			style="border-color: var(--border)"
		>
			<h2 class="text-sm font-semibold">Traffic through this server</h2>
			{#if isAdmin}
				<button class="btn btn-default btn-sm" onclick={askResetHere}>
					<Icon name="refresh" size={13} />
					Reset
				</button>
			{/if}
		</header>
		<div class="grid grid-cols-2 sm:grid-cols-4">
			{#each windows as w, i (w.key)}
				<div
					class="px-4 py-3 {i % 2 === 1 ? 'border-l' : ''} {i >= 2
						? 'border-t sm:border-t-0'
						: ''} {i === 2 ? 'sm:border-l' : ''}"
					style="border-color: var(--border)"
				>
					<div class="text-xs" style="color: var(--text-muted)">{w.label}</div>
					<div class="text-lg leading-tight font-semibold tabular-nums">
						{bytes(status.recent[w.key])}
					</div>
				</div>
			{/each}
		</div>
	</section>

	<div class="mb-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
		<!-- Five cards fill no two- or three-column grid evenly, so this one widens
		     to keep the rows whole until there is room for all five in one. -->
		<div class="sm:col-span-2 xl:col-span-1">
			<StatCard
				label="Total usage"
				value={bytes(status.usage)}
				hint="{status.groups} group{status.groups === 1 ? '' : 's'}"
				icon="globe"
				accent="var(--warning)"
			/>
		</div>
		<StatCard
			label="Active"
			value={String(status.peers.active)}
			accent="var(--success)"
			icon="power"
		/>
		<StatCard
			label="Disabled"
			value={String(status.peers.disabled)}
			accent="var(--text-muted)"
			icon="power"
		/>
		<StatCard
			label="Expired"
			value={String(status.peers.expired)}
			accent="var(--danger)"
			icon="info"
		/>
		<StatCard
			label="Quota reached"
			value={String(status.peers.quota)}
			accent="var(--warning)"
			icon="filter"
		/>
	</div>
{/if}

{#if showFleet}
	<section class="card mb-4 overflow-hidden">
		<header
			class="flex items-center justify-between border-b px-4 py-2.5"
			style="border-color: var(--border)"
		>
			<h2 class="text-sm font-semibold">Servers</h2>
			<div class="flex items-center gap-3">
				{#if !isNode && nodeRows.length > 0}
					<button class="btn btn-ghost btn-sm" onclick={askResetAll}>
						<Icon name="refresh" size={13} />
						Reset all traffic
					</button>
				{/if}
				<a href="/nodes" class="text-xs hover:underline" style="color: var(--primary)">Manage</a>
			</div>
		</header>
		<div class="overflow-x-auto">
			<table class="w-full min-w-[860px] text-sm">
				<thead class="text-left text-xs whitespace-nowrap" style="color: var(--text-muted)">
					<tr>
						<th class="px-4 py-2 font-medium">Server</th>
						<th class="w-[80px] px-4 py-2 font-medium">Peers</th>
						<th class="w-[80px] px-4 py-2 font-medium">Online</th>
						<th class="w-[100px] px-4 py-2 text-right font-medium">Last hour</th>
						<th class="w-[100px] px-4 py-2 text-right font-medium">Last 24h</th>
						<th class="w-[100px] px-4 py-2 text-right font-medium">Last 7d</th>
						<th class="w-[100px] px-4 py-2 text-right font-medium">Last 30d</th>
						<th class="w-[110px] px-4 py-2 font-medium">Last sync</th>
						<th class="w-[48px] px-2 py-2"><span class="sr-only">Actions</span></th>
					</tr>
				</thead>
				<tbody>
					{#each fleet as node (node.id)}
						<tr class="border-t" style="border-color: var(--border)">
							<td class="px-4 py-2">
								<span class="flex items-center gap-2">
									<span
										class="inline-block h-1.5 w-1.5 shrink-0 rounded-full"
										style="background: {node.online ? 'var(--success)' : 'var(--danger)'}"
									></span>
									<span class="truncate font-medium">{node.name || node.id.slice(0, 12)}</span>
									<span
										class="tag shrink-0 text-[10px]"
										style="background: var(--surface-3); color: var(--text-muted)"
									>
										{node.role === 'local' ? 'this server' : node.role}
									</span>
								</span>
							</td>
							<td class="w-[80px] px-4 py-2 tabular-nums" style="color: var(--text-muted)">
								{node.peerCount}
							</td>
							<td class="w-[80px] px-4 py-2 tabular-nums" style="color: var(--text-muted)">
								{node.onlinePeers}
							</td>
							{#each windows as w (w.key)}
								<td
									class="w-[100px] px-4 py-2 text-right font-mono text-xs whitespace-nowrap tabular-nums"
									style="color: var(--text-muted)"
								>
									{bytes(node.recent[w.key] ?? 0)}
								</td>
							{/each}
							<td class="w-[110px] px-4 py-2 text-xs tabular-nums" style="color: var(--text-muted)">
								{node.role === 'local' ? '—' : relative(node.lastSeenAt)}
							</td>
							<td class="w-[48px] px-2 py-1">
								<div class="flex justify-end">
									<!-- This server resets itself; a master resets a node through
									     its sync. The server a node follows is not the node's to
									     reset. -->
									{#if node.role === 'local' || (node.role === 'node' && !isNode)}
										<button
											class="btn btn-ghost btn-icon"
											onclick={() => (node.role === 'local' ? askResetHere() : askResetNode(node))}
											title="Reset this server's traffic counters"
											aria-label="Reset traffic on {node.name || node.id.slice(0, 12)}"
										>
											<Icon name="refresh" size={14} />
										</button>
									{/if}
								</div>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</section>
{/if}

<div class="mb-4 grid gap-4 xl:grid-cols-2">
	<section class="card overflow-hidden">
		<header
			class="flex items-center justify-between border-b px-4 py-2.5"
			style="border-color: var(--border)"
		>
			<h2 class="text-sm font-semibold">Busiest right now</h2>
			<a href="/peers?sort=speed&order=desc" class="text-xs" style="color: var(--primary)"
				>View all</a
			>
		</header>
		{#if busiest.length === 0}
			<p class="px-4 py-8 text-center text-xs" style="color: var(--text-faint)">No traffic yet</p>
		{:else}
			<ul>
				{#each busiest as peer (peer.id)}
					<li
						class="flex items-center justify-between gap-3 border-b px-4 py-2.5 last:border-0"
						style="border-color: var(--border)"
					>
						<div class="min-w-0">
							<a
								href="/peers?open={encodeURIComponent(peer.id)}"
								class="truncate text-sm hover:underline"
							>
								{peer.name}
							</a>
							<div class="font-mono text-[11px]" style="color: var(--text-faint)">
								{peer.allowedIps}
							</div>
						</div>
						<div class="shrink-0 text-right font-mono text-xs">
							<div style="color: var(--success)">↓ {speed(peer.txSpeed)}</div>
							<div style="color: var(--info)">↑ {speed(peer.rxSpeed)}</div>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>

	<section class="card overflow-hidden">
		<header
			class="flex items-center justify-between border-b px-4 py-2.5"
			style="border-color: var(--border)"
		>
			<h2 class="text-sm font-semibold">Expiring soonest</h2>
			<a href="/peers?sort=expiry" class="text-xs" style="color: var(--primary)">View all</a>
		</header>
		{#if expiring.length === 0}
			<p class="px-4 py-8 text-center text-xs" style="color: var(--text-faint)">
				Nothing with an expiry date
			</p>
		{:else}
			<ul>
				{#each expiring as peer (peer.id)}
					<li
						class="flex items-center justify-between gap-3 border-b px-4 py-2.5 last:border-0"
						style="border-color: var(--border)"
					>
						<div class="min-w-0">
							<a
								href="/peers?open={encodeURIComponent(peer.id)}"
								class="truncate text-sm hover:underline"
							>
								{peer.name}
							</a>
							<div class="text-[11px]" style="color: var(--text-faint)">
								{handshake(peer.lastHandshakeAt)}
							</div>
						</div>
						<div class="shrink-0 text-right">
							<StatusTag status={peer.status} online={peer.online} />
							<div class="mt-0.5 text-[11px]" style="color: var(--text-muted)">
								{new Date(peer.expiresAt).toLocaleDateString()}
							</div>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
</div>

{#if app.session?.isAdmin}
	<NetworkRules />
{/if}

<Confirm
	open={confirming !== null}
	title={confirming?.title ?? ''}
	message={confirming?.message ?? ''}
	confirmLabel="Reset"
	danger
	busy={confirmBusy}
	onconfirm={confirmReset}
	oncancel={() => (confirming = null)}
/>
