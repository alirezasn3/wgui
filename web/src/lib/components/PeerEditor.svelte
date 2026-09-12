<script lang="ts">
	import { api, type PeerInput } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import { bytesToGib, gibToBytes } from '$lib/format';
	import type { Group, Peer } from '$lib/types';
	import ExpiryInput from './ExpiryInput.svelte';
	import LimitInput from './LimitInput.svelte';
	import Modal from './Modal.svelte';

	interface Props {
		open: boolean;
		/** null creates a new peer, otherwise the peer being edited. */
		peer: Peer | null;
		groups: Group[];
		distributors: Peer[];
		onclose: () => void;
		onsaved: (peer: Peer) => void;
	}
	let { open, peer, groups, distributors, onclose, onsaved }: Props = $props();

	let name = $state('');
	let role = $state('user');
	let ownerId = $state('');
	let groupId = $state(0);
	let usageGib = $state(0);
	/** The expiry in force; the control below edits it as a number of days. */
	let expiresAt = $state(0);
	// Both limits are only sent when the operator actually edits them, so that
	// opening a dialog and saving something else cannot shift them: bytes lose
	// precision through a 2-decimal GiB field, and an expiry re-based from "days
	// from now" creeps forward by however long the dialog was open.
	let expiryDays = $state<number | null>(null);
	let usageDirty = $state(false);
	let clientEndpoint = $state('');
	let preferredEndpoint = $state('');
	let note = $state('');
	let disabled = $state(false);
	let saving = $state(false);
	let error = $state('');

	let editing = $derived(peer !== null);
	let endpoints = $derived(app.session?.endpoints ?? []);

	// A group owns its members' allowance and expiry outright, so those two
	// fields are read-only while one is selected — including a group picked in
	// this very dialog, which is why it keys off the form and not the saved peer.
	// Membership is decided by the id alone: a group the picker never loaded
	// still governs.
	let grouped = $derived(groupId !== 0);
	let governingName = $derived(
		groups.find((g) => g.id === groupId)?.name || peer?.groupName || 'Its group'
	);

	// Reset the form whenever the dialog is opened for a different peer.
	$effect(() => {
		if (!open) return;
		const defaults = app.session?.peerDefaults;
		name = peer?.name ?? '';
		role = peer?.role ?? defaults?.role ?? 'user';
		ownerId = peer?.ownerId ?? '';
		groupId = peer?.groupId ?? 0;
		usageGib = bytesToGib(peer ? peer.allowedUsage : (defaults?.allowedUsageBytes ?? 0));
		usageDirty = !peer;
		expiresAt = peer
			? peer.expiresAt
			: defaults?.expiryDays
				? Date.now() + defaults.expiryDays * 86_400_000
				: 0;
		expiryDays = peer ? null : (defaults?.expiryDays ?? 0);
		clientEndpoint = peer?.clientEndpoint ?? '';
		preferredEndpoint = peer?.preferredEndpoint ?? '';
		note = peer?.note ?? '';
		disabled = peer?.manuallyDisabled ?? false;
		error = '';
	});

	async function save() {
		error = '';
		if (!name.trim()) {
			error = 'A name is required';
			return;
		}

		const input: PeerInput = {
			name: name.trim(),
			groupId,
			clientEndpoint,
			preferredEndpoint,
			note,
			manuallyDisabled: disabled
		};
		// Leave a member's own allowance and expiry exactly as they are: the
		// group decides while it is in one, and these take over again untouched
		// if it ever leaves.
		if (!grouped) {
			if (usageDirty) input.allowedUsage = gibToBytes(usageGib);
			if (expiryDays !== null) input.expiryDays = expiryDays;
		}
		// Role and ownership are admin-only, and the API rejects them from
		// anyone else, so only send them when they can actually be applied.
		if (app.session?.isAdmin) {
			input.role = role;
			input.ownerId = ownerId;
		}

		saving = true;
		try {
			const saved = peer ? await api.updatePeer(peer.id, input) : await api.createPeer(input);
			app.success(peer ? `Updated ${saved.name}` : `Created ${saved.name}`);
			onsaved(saved);
			onclose();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}
</script>

<Modal
	{open}
	title={editing ? `Edit ${peer?.name}` : 'New peer'}
	subtitle={editing ? peer?.allowedIps : 'A key pair and tunnel address are assigned automatically'}
	width="34rem"
	{onclose}
>
	<div class="space-y-4">
		<div>
			<label class="label" for="peer-name">Name</label>
			<input
				id="peer-name"
				class="field"
				bind:value={name}
				placeholder="e.g. laptop-anna"
				autocomplete="off"
			/>
		</div>

		<div class="grid gap-4 sm:grid-cols-2">
			<div>
				<label class="label" for="peer-usage">Data allowance</label>
				<LimitInput
					id="peer-usage"
					value={usageGib}
					unit="GiB"
					step={1}
					disabled={grouped}
					onchange={(v) => {
						usageGib = v;
						usageDirty = true;
					}}
				/>
			</div>
			<div>
				<label class="label" for="peer-expiry">Expires</label>
				<ExpiryInput
					id="peer-expiry"
					value={expiresAt}
					disabled={grouped}
					onchange={(v) => (expiryDays = v)}
				/>
			</div>
		</div>

		{#if grouped}
			<p class="-mt-2 text-[11px]" style="color: var(--text-faint)">
				<strong>{governingName}</strong> sets the allowance and expiry for everyone in it, so these two
				are read-only. The peer's own values are kept and apply again if it leaves the group.
			</p>
		{/if}

		<div class="grid gap-4 sm:grid-cols-2">
			<div>
				<label class="label" for="peer-group">Group</label>
				<select id="peer-group" class="field" bind:value={groupId}>
					<option value={0}>No group</option>
					{#each groups as group (group.id)}
						<option value={group.id}>{group.name}</option>
					{/each}
				</select>
			</div>

			{#if app.session?.isAdmin}
				<div>
					<label class="label" for="peer-role">Role</label>
					<select id="peer-role" class="field" bind:value={role}>
						<option value="user">User — sees only itself</option>
						<option value="distributor">Distributor — manages its own peers</option>
						<option value="admin">Admin — full access</option>
					</select>
				</div>
			{/if}
		</div>

		{#if app.session?.isAdmin}
			<div>
				<label class="label" for="peer-owner">Owned by</label>
				<select id="peer-owner" class="field" bind:value={ownerId}>
					<option value="">Nobody (managed by admins)</option>
					{#each distributors as d (d.id)}
						<option value={d.id}>{d.name}</option>
					{/each}
				</select>
			</div>
		{/if}

		<div>
			<label class="label" for="peer-endpoint">Server endpoint for this peer</label>
			<select id="peer-endpoint" class="field" bind:value={clientEndpoint}>
				<option value="">Use the default endpoint</option>
				{#each endpoints as endpoint (endpoint)}
					<option value={endpoint}>{endpoint}</option>
				{/each}
			</select>
			<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
				Which address this peer's configuration and QR code point at.
			</p>
		</div>

		<details class="text-sm">
			<summary class="cursor-pointer text-xs" style="color: var(--text-muted)">Advanced</summary>
			<div class="mt-3 space-y-4">
				<div>
					<label class="label" for="peer-pinned">Pinned remote endpoint</label>
					<input
						id="peer-pinned"
						class="field font-mono text-xs"
						bind:value={preferredEndpoint}
						placeholder="host:port — leave empty to learn it from handshakes"
						autocomplete="off"
					/>
					<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
						Forces the server to send this peer's traffic to a fixed address, for site-to-site links
						and other static clients.
					</p>
				</div>
				<div>
					<label class="label" for="peer-note">Note</label>
					<textarea id="peer-note" class="field" rows="2" bind:value={note}></textarea>
				</div>
				<label class="flex cursor-pointer items-center gap-2 text-sm">
					<input type="checkbox" class="accent-[var(--primary)]" bind:checked={disabled} />
					Disabled
				</label>
			</div>
		</details>

		{#if error}
			<p class="text-sm" style="color: var(--danger)">{error}</p>
		{/if}
	</div>

	{#snippet footer()}
		<button class="btn btn-default" onclick={onclose} disabled={saving}>Cancel</button>
		<button class="btn btn-primary" onclick={save} disabled={saving}>
			{saving ? 'Saving…' : editing ? 'Save changes' : 'Create peer'}
		</button>
	{/snippet}
</Modal>
