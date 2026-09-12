<script lang="ts">
	import { gibToBytes } from '$lib/format';
	import type { Group, PeerBulkAction } from '$lib/types';
	import LimitInput from './LimitInput.svelte';
	import Modal from './Modal.svelte';

	interface Props {
		open: boolean;
		action: PeerBulkAction | null;
		count: number;
		groups: Group[];
		canSetRole: boolean;
		busy: boolean;
		onclose: () => void;
		onapply: (values: {
			allowedUsage?: number;
			expiryDays?: number;
			groupId?: number;
			role?: string;
		}) => void;
	}
	let { open, action, count, groups, canSetRole, busy, onclose, onapply }: Props = $props();

	let usageGib = $state(0);
	let expiryDays = $state(30);
	let groupId = $state(0);
	let role = $state('user');

	const titles: Record<string, string> = {
		setUsage: 'Set data allowance',
		setExpiry: 'Set expiry',
		setGroup: 'Move to group',
		setRole: 'Change role'
	};
</script>

<Modal
	{open}
	title={action ? (titles[action] ?? 'Bulk edit') : ''}
	subtitle="Applies to {count} selected peer{count === 1 ? '' : 's'}"
	width="26rem"
	{onclose}
>
	{#if action === 'setUsage'}
		<label class="label" for="bulk-usage">Data allowance</label>
		<LimitInput id="bulk-usage" value={usageGib} unit="GiB" onchange={(v) => (usageGib = v)} />
		<p class="mt-2 text-xs" style="color: var(--text-faint)">
			This replaces each peer's own allowance. Usage already recorded is kept. Peers in a group keep
			following their group's allowance — this is only what they fall back to if they leave.
		</p>
	{:else if action === 'setExpiry'}
		<label class="label" for="bulk-expiry">Expires in</label>
		<LimitInput
			id="bulk-expiry"
			value={expiryDays}
			unit="days"
			unlimitedLabel="Never"
			onchange={(v) => (expiryDays = v)}
		/>
		<p class="mt-2 text-xs" style="color: var(--text-faint)">
			Counted from now, so every selected peer gets the same new expiry date. Peers in a group keep
			following their group's expiry — this is only what they fall back to if they leave.
		</p>
	{:else if action === 'setGroup'}
		<label class="label" for="bulk-group">Group</label>
		<select id="bulk-group" class="field" bind:value={groupId}>
			<option value={0}>Remove from group</option>
			{#each groups as group (group.id)}
				<option value={group.id}>{group.name}</option>
			{/each}
		</select>
	{:else if action === 'setRole' && canSetRole}
		<label class="label" for="bulk-role">Role</label>
		<select id="bulk-role" class="field" bind:value={role}>
			<option value="user">User</option>
			<option value="distributor">Distributor</option>
			<option value="admin">Admin</option>
		</select>
	{/if}

	{#snippet footer()}
		<button class="btn btn-default" onclick={onclose} disabled={busy}>Cancel</button>
		<button
			class="btn btn-primary"
			disabled={busy}
			onclick={() =>
				onapply({
					allowedUsage: gibToBytes(usageGib),
					expiryDays,
					groupId,
					role
				})}
		>
			{busy ? 'Applying…' : 'Apply'}
		</button>
	{/snippet}
</Modal>
