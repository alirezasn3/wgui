<script lang="ts">
	import { api } from '$lib/api';
	import { app, poll } from '$lib/app.svelte';
	import Confirm from '$lib/components/Confirm.svelte';
	import CopyButton from '$lib/components/CopyButton.svelte';
	import Empty from '$lib/components/Empty.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import { absolute, relative } from '$lib/format';
	import type { Node } from '$lib/types';
	import { onMount } from 'svelte';

	let nodes = $state<Node[]>([]);
	let secret = $state('');
	let fingerprint = $state('');
	let revealed = $state(false);
	let loadError = $state('');
	let firstLoad = $state(true);
	let confirming = $state<{
		title: string;
		message: string;
		confirmLabel: string;
		run: () => Promise<void>;
	} | null>(null);
	let confirmBusy = $state(false);

	// This server is in the list so the dashboard can show the fleet side by
	// side, but a page about other servers should not list the one you are
	// looking at.
	let others = $derived(nodes.filter((n) => n.role !== 'local'));
	let isNode = $derived(app.session?.isNode ?? false);

	async function load() {
		try {
			nodes = (await api.nodes()).nodes;
			loadError = '';
		} catch (e) {
			loadError = e instanceof Error ? e.message : String(e);
		} finally {
			firstLoad = false;
		}
	}

	onMount(() => {
		api
			.syncSecret()
			.then((r) => {
				secret = r.secret;
				fingerprint = r.fingerprint;
			})
			.catch(() => {});
		return poll(load, 5000);
	});

	function askRotate() {
		confirming = {
			title: 'Replace the sync secret?',
			message:
				'Every node stops syncing the moment this changes, and stays stopped until its config.json carries the new secret and it is restarted. Their peers keep working in the meantime — only the exchange stops.',
			confirmLabel: 'Replace',
			run: async () => {
				const rotated = await api.rotateSyncSecret();
				secret = rotated.secret;
				fingerprint = rotated.fingerprint;
				revealed = true;
				app.success('Sync secret replaced');
			}
		};
	}

	function askForget(node: Node) {
		confirming = {
			title: `Forget ${node.name || node.id.slice(0, 12)}?`,
			message:
				'Everything it counted goes with it, so every peer it served loses that share of its usage. It comes back on its own if it syncs again.',
			confirmLabel: 'Forget',
			run: async () => {
				await api.forgetNode(node.id);
				app.success('Node forgotten');
				await load();
			}
		};
	}

	async function confirmRun() {
		if (!confirming) return;
		confirmBusy = true;
		try {
			await confirming.run();
			confirming = null;
		} catch (e) {
			app.fail(e);
		} finally {
			confirmBusy = false;
		}
	}

	/**
	 * What to paste into a new node's config.json.
	 *
	 * The fingerprint is included rather than left as an exercise. A node
	 * verifies the certificate properly and refuses outright when it cannot,
	 * and a self-signed certificate — or a panel reached under a name that
	 * certificate does not carry — is exactly the case that fails.
	 */
	let snippet = $derived(
		JSON.stringify(
			{
				master: {
					url: location.origin,
					secret: revealed ? secret : '<the secret above>',
					...(fingerprint ? { fingerprint } : {})
				}
			},
			null,
			2
		)
	);
</script>

<div class="mb-4">
	<h1 class="text-xl font-semibold">Nodes</h1>
	<p class="text-xs" style="color: var(--text-muted)">
		{#if isNode}
			The server this one takes its peers from
		{:else}
			Other servers carrying these same peers, and counting usage against the same allowances
		{/if}
	</p>
</div>

{#if loadError}
	<div class="card mb-3 p-3 text-sm" style="border-color: var(--danger); color: var(--danger)">
		{loadError}
	</div>
{/if}

<div class="card mb-3 overflow-hidden">
	<div class="overflow-x-auto">
		<table class="w-full min-w-[680px] table-fixed text-sm">
			<thead
				class="text-left text-xs"
				style="background: var(--surface-2); color: var(--text-muted)"
			>
				<tr>
					<th class="px-3 py-2 font-medium">Node</th>
					<th class="w-[104px] px-3 py-2 font-medium">Status</th>
					<th class="w-[140px] px-3 py-2 font-medium">Last sync</th>
					<th class="w-[130px] px-3 py-2 font-medium">Serving</th>
					<th class="w-[160px] px-3 py-2 font-medium max-lg:hidden">Version</th>
					<th class="w-[100px] px-3 py-2"></th>
				</tr>
			</thead>
			<tbody>
				{#each others as node (node.id)}
					<tr class="border-t" style="border-color: var(--border)">
						<td class="px-3 py-2">
							<div class="truncate font-medium">{node.name || 'unnamed'}</div>
							<div class="truncate font-mono text-[11px]" style="color: var(--text-faint)">
								{node.address} · {node.id.slice(0, 16)}
							</div>
						</td>
						<td class="w-[104px] px-3 py-2">
							<span
								class="tag"
								style="background: var(--surface-3); color: {node.online
									? 'var(--success)'
									: 'var(--danger)'}"
							>
								{node.online ? 'Online' : 'Offline'}
							</span>
						</td>
						<td
							class="w-[140px] px-3 py-2 text-xs tabular-nums"
							style="color: var(--text-muted)"
							title={absolute(node.lastSeenAt)}
						>
							{relative(node.lastSeenAt)}
						</td>
						<td class="w-[130px] px-3 py-2 text-xs tabular-nums" style="color: var(--text-muted)">
							{node.peerCount} peer{node.peerCount === 1 ? '' : 's'}
						</td>
						<td
							class="w-[160px] px-3 py-2 font-mono text-[11px] max-lg:hidden"
							style="color: var(--text-faint)"
						>
							<span class="block truncate">{node.version}</span>
						</td>
						<td class="w-[100px] px-3 py-2">
							<div class="flex justify-end">
								{#if node.role !== 'master'}
									<!-- Forgetting the master would only drop what it has counted and
									     invite it straight back on the next sync. -->
									<button
										class="btn btn-ghost btn-icon"
										style="color: var(--danger)"
										onclick={() => askForget(node)}
										title="Forget this node"
										aria-label="Forget {node.name}"
									>
										<Icon name="trash" size={15} />
									</button>
								{/if}
							</div>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	{#if others.length === 0 && !firstLoad}
		<Empty
			title={isNode ? 'Not syncing yet' : 'No nodes yet'}
			hint={isNode
				? 'This server has not heard back from its master. Check the master block in its config.json.'
				: 'A node appears here on its own the first time it syncs. There is nothing to register.'}
		/>
	{/if}
</div>

{#if !isNode}
	<div class="card p-4">
		<h2 class="mb-1 text-sm font-semibold">Adding a node</h2>
		<p class="mb-3 text-xs" style="color: var(--text-muted)">
			Install wgui on the other server as usual, but give its <span class="font-mono"
				>config.json</span
			>
			a <span class="font-mono">master</span> block before the first start. It needs no database of its
			own and creates no admin peer: it is given this server's peers, the admin among them, within seconds.
		</p>

		<div class="mb-3">
			<div class="label mb-1">Sync secret</div>
			<div class="flex flex-wrap items-center gap-2">
				<code
					class="flex-1 truncate rounded-md px-3 py-2 font-mono text-xs"
					style="background: var(--surface-2)"
				>
					{revealed ? secret : '•'.repeat(64)}
				</code>
				<button class="btn btn-default btn-sm" onclick={() => (revealed = !revealed)}>
					{revealed ? 'Hide' : 'Reveal'}
				</button>
				<CopyButton value={secret} label="Copy" />
				<button class="btn btn-default btn-sm" onclick={askRotate}>
					<Icon name="refresh" size={13} /> Replace
				</button>
			</div>
			<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
				Every node presents this. Replacing it locks all of them out until each is given the new
				one.
			</p>
		</div>

		{#if fingerprint}
			<div class="mb-3">
				<div class="label mb-1">This panel's certificate</div>
				<div class="flex flex-wrap items-center gap-2">
					<code
						class="flex-1 truncate rounded-md px-3 py-2 font-mono text-[11px]"
						style="background: var(--surface-2)"
					>
						{fingerprint}
					</code>
					<CopyButton value={fingerprint} label="Copy" />
				</div>
				<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
					A node pins this instead of trusting an authority, so it keeps working under any name this
					panel answers on.
				</p>
			</div>
		{/if}

		<div class="label mb-1">config.json on the node</div>
		<pre
			class="overflow-x-auto rounded-md px-3 py-2 font-mono text-xs"
			style="background: var(--surface-2)">{snippet}</pre>
		<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
			Drop <span class="font-mono">"fingerprint"</span> if this panel serves a certificate from a real
			authority under the name above; keep it otherwise.
		</p>
	</div>
{/if}

<Confirm
	open={confirming !== null}
	title={confirming?.title ?? ''}
	message={confirming?.message ?? ''}
	confirmLabel={confirming?.confirmLabel ?? 'Confirm'}
	danger
	busy={confirmBusy}
	onconfirm={confirmRun}
	oncancel={() => (confirming = null)}
/>
