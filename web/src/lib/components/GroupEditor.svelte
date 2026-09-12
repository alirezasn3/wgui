<script lang="ts">
	import { api, type GroupInput } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import { bytesToGib, gibToBytes } from '$lib/format';
	import type { Group } from '$lib/types';
	import ExpiryInput from './ExpiryInput.svelte';
	import LimitInput from './LimitInput.svelte';
	import Modal from './Modal.svelte';

	interface Props {
		open: boolean;
		group: Group | null;
		onclose: () => void;
		onsaved: (group: Group) => void;
	}
	let { open, group, onclose, onsaved }: Props = $props();

	let name = $state('');
	let usageGib = $state(0);
	/** The expiry in force; the control below edits it as a number of days. */
	let expiresAt = $state(0);
	// Both limits are only sent when the operator actually edits them. Round
	// tripping them on every save would quietly shift them: bytes lose precision
	// through a 2-decimal GiB field, and an expiry re-based from "days from now"
	// creeps forward by however long the dialog was open.
	let expiryDays = $state<number | null>(null);
	let usageDirty = $state(false);
	let note = $state('');
	let disabled = $state(false);
	let saving = $state(false);
	let error = $state('');

	$effect(() => {
		if (!open) return;
		name = group?.name ?? '';
		usageGib = bytesToGib(group?.allowedUsage ?? 0);
		usageDirty = !group;
		expiresAt = group ? group.expiresAt : Date.now() + 30 * 86_400_000;
		expiryDays = group ? null : 30;
		note = group?.note ?? '';
		disabled = group?.manuallyDisabled ?? false;
		error = '';
	});

	async function save() {
		error = '';
		if (!name.trim()) {
			error = 'A name is required';
			return;
		}

		const input: GroupInput = {
			name: name.trim(),
			note,
			manuallyDisabled: disabled
		};
		if (usageDirty) input.allowedUsage = gibToBytes(usageGib);
		if (expiryDays !== null) input.expiryDays = expiryDays;

		saving = true;
		try {
			const saved = group ? await api.updateGroup(group.id, input) : await api.createGroup(input);
			app.success(group ? `Updated ${saved.name}` : `Created ${saved.name}`);
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
	title={group ? `Edit ${group.name}` : 'New group'}
	subtitle="The allowance and expiry below apply to all members together"
	width="28rem"
	{onclose}
>
	<div class="space-y-4">
		<div>
			<label class="label" for="group-name">Name</label>
			<input
				id="group-name"
				class="field"
				bind:value={name}
				placeholder="e.g. household"
				autocomplete="off"
			/>
		</div>

		<div>
			<label class="label" for="group-usage">Shared data allowance</label>
			<LimitInput
				id="group-usage"
				value={usageGib}
				unit="GiB"
				onchange={(v) => {
					usageGib = v;
					usageDirty = true;
				}}
			/>
			<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
				Counted across every member. When it runs out, they are all cut off.
			</p>
		</div>

		<div>
			<label class="label" for="group-expiry">Expires</label>
			<ExpiryInput id="group-expiry" value={expiresAt} onchange={(v) => (expiryDays = v)} />
		</div>

		<div>
			<label class="label" for="group-note">Note</label>
			<textarea id="group-note" class="field" rows="2" bind:value={note}></textarea>
		</div>

		<label class="flex cursor-pointer items-center gap-2 text-sm">
			<input type="checkbox" class="accent-[var(--primary)]" bind:checked={disabled} />
			Disabled — cuts off every member
		</label>

		{#if error}
			<p class="text-sm" style="color: var(--danger)">{error}</p>
		{/if}
	</div>

	{#snippet footer()}
		<button class="btn btn-default" onclick={onclose} disabled={saving}>Cancel</button>
		<button class="btn btn-primary" onclick={save} disabled={saving}>
			{saving ? 'Saving…' : group ? 'Save changes' : 'Create group'}
		</button>
	{/snippet}
</Modal>
