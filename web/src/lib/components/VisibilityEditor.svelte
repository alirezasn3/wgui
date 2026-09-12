<script lang="ts">
	import { api } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import type { Peer } from '$lib/types';
	import Icon from './Icon.svelte';
	import Modal from './Modal.svelte';

	interface Props {
		open: boolean;
		/** The user whose view is being widened. */
		peer: Peer;
		onclose: () => void;
		onsaved: () => void;
	}
	let { open, peer, onclose, onsaved }: Props = $props();

	let candidates = $state<Peer[]>([]);
	let selected = $state<Set<string>>(new Set());
	let search = $state('');
	let loading = $state(false);
	let saving = $state(false);
	let error = $state('');

	// Keyed on the peer's identity, not the object: the object is replaced on
	// every poll of the list behind the dialog, and reloading on each one threw
	// away whatever the operator had just ticked.
	let loadedFor = $state<string | null>(null);

	$effect(() => {
		const id = open ? peer.id : null;
		if (id === loadedFor) return;
		loadedFor = id;
		if (id) void load();
	});

	async function load() {
		loading = true;
		error = '';
		try {
			// Only peers the current operator can see may be shared, which is what
			// the peers endpoint already returns.
			const [list, grants] = await Promise.all([
				api.peers({ pageSize: 500, sort: 'name' }),
				api.visibility(peer.id)
			]);
			candidates = list.peers.filter((p) => p.id !== peer.id);
			selected = new Set(grants.targets);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	function toggle(id: string) {
		const next = new Set(selected);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		selected = next;
	}

	let visible = $derived(
		candidates.filter((p) => {
			const q = search.trim().toLowerCase();
			return !q || p.name.toLowerCase().includes(q) || p.allowedIps.includes(q);
		})
	);

	async function save() {
		saving = true;
		error = '';
		try {
			await api.setVisibility(peer.id, [...selected]);
			app.success(
				selected.size === 0
					? `${peer.name} can now only see itself`
					: `${peer.name} can now see ${selected.size} other peer${selected.size === 1 ? '' : 's'}`
			);
			onsaved();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}
</script>

<Modal
	{open}
	title="Shared with {peer.name}"
	subtitle="Pick the peers this user is allowed to see. It always sees itself."
	width="30rem"
	{onclose}
>
	<div class="space-y-3">
		<div class="relative">
			<span class="absolute top-1/2 left-2.5 -translate-y-1/2" style="color: var(--text-faint)">
				<Icon name="search" size={14} />
			</span>
			<input class="field pl-8" bind:value={search} placeholder="Search peers" autocomplete="off" />
		</div>

		{#if loading}
			<p class="py-6 text-center text-xs" style="color: var(--text-faint)">Loading…</p>
		{:else if visible.length === 0}
			<p class="py-6 text-center text-xs" style="color: var(--text-faint)">No other peers</p>
		{:else}
			<ul class="max-h-72 overflow-y-auto rounded-md border" style="border-color: var(--border)">
				{#each visible as candidate (candidate.id)}
					<li class="border-b last:border-0" style="border-color: var(--border)">
						<label
							class="flex cursor-pointer items-center gap-2.5 px-3 py-2 text-sm transition-colors hover:bg-[var(--surface-2)]"
						>
							<input
								type="checkbox"
								class="accent-[var(--primary)]"
								checked={selected.has(candidate.id)}
								onchange={() => toggle(candidate.id)}
							/>
							<span class="min-w-0 flex-1 truncate">{candidate.name}</span>
							<span class="font-mono text-[11px]" style="color: var(--text-faint)">
								{candidate.allowedIps}
							</span>
						</label>
					</li>
				{/each}
			</ul>
		{/if}

		{#if error}
			<p class="text-sm" style="color: var(--danger)">{error}</p>
		{/if}
	</div>

	{#snippet footer()}
		<button class="btn btn-default" onclick={onclose} disabled={saving}>Cancel</button>
		<button class="btn btn-primary" onclick={save} disabled={saving || loading}>
			{saving ? 'Saving…' : `Save (${selected.size})`}
		</button>
	{/snippet}
</Modal>
