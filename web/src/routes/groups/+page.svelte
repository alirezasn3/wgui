<script lang="ts">
	import { api } from '$lib/api';
	import { app, poll } from '$lib/app.svelte';
	import BulkBar from '$lib/components/BulkBar.svelte';
	import Confirm from '$lib/components/Confirm.svelte';
	import Empty from '$lib/components/Empty.svelte';
	import GroupEditor from '$lib/components/GroupEditor.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import SortHeader from '$lib/components/SortHeader.svelte';
	import StatusTag from '$lib/components/StatusTag.svelte';
	import UsageBar from '$lib/components/UsageBar.svelte';
	import { relative } from '$lib/format';
	import type { Group } from '$lib/types';
	import { onMount } from 'svelte';

	let groups = $state<Group[]>([]);
	let search = $state('');
	let status = $state('');
	let sort = $state('name');
	let order = $state<'asc' | 'desc'>('asc');
	let loadError = $state('');
	let firstLoad = $state(true);

	let selection = $state<Set<number>>(new Set());
	let editing = $state<Group | null>(null);
	let editorOpen = $state(false);
	let confirming = $state<{
		title: string;
		message: string;
		confirmLabel?: string;
		danger?: boolean;
		run: () => Promise<void>;
	} | null>(null);
	let confirmBusy = $state(false);
	let bulkBusy = $state(false);

	let isAdmin = $derived(app.session?.isAdmin ?? false);
	let canWrite = $derived(app.session?.canWrite ?? false);

	async function load() {
		try {
			const res = await api.groups({
				search: search || undefined,
				status: status || undefined,
				sort,
				order,
				pageSize: 200
			});
			groups = res.groups;
			const present = new Set(res.groups.map((g) => g.id));
			if ([...selection].some((id) => !present.has(id))) {
				selection = new Set([...selection].filter((id) => present.has(id)));
			}
			loadError = '';
		} catch (e) {
			loadError = e instanceof Error ? e.message : String(e);
		} finally {
			firstLoad = false;
		}
	}

	onMount(() => poll(load, 3000));

	function onSort(field: string) {
		if (sort === field) order = order === 'asc' ? 'desc' : 'asc';
		else {
			sort = field;
			order = 'asc';
		}
		void load();
	}

	let allSelected = $derived(groups.length > 0 && groups.every((g) => selection.has(g.id)));

	function toggleAll() {
		selection = allSelected ? new Set() : new Set(groups.map((g) => g.id));
	}

	function toggle(id: number) {
		const next = new Set(selection);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		selection = next;
	}

	async function runBulk(action: string) {
		bulkBusy = true;
		try {
			const res = await api.bulkGroups({ ids: [...selection], action });
			app.success(`Updated ${res.affected} group${res.affected === 1 ? '' : 's'}`);
			selection = new Set();
			await load();
		} catch (e) {
			app.fail(e);
		} finally {
			bulkBusy = false;
		}
	}

	function askResetGroup(group: Group) {
		const members = group.peerCount === 1 ? "member's" : "members'";
		confirming = {
			title: `Reset usage for ${group.name}?`,
			message: `The ${group.peerCount} ${members} counters go back to zero, so the group's shared allowance starts again. Nothing else about them changes.`,
			confirmLabel: 'Reset',
			danger: false,
			run: async () => {
				await api.updateGroup(group.id, { resetUsage: true });
				app.success(`Reset usage for ${group.name}`);
				await load();
			}
		};
	}

	function askDelete(group: Group) {
		confirming = {
			title: `Delete ${group.name}?`,
			message:
				group.peerCount > 0
					? `Its ${group.peerCount} member${group.peerCount === 1 ? '' : 's'} are not deleted: they leave the group and fall back to their own allowance and expiry.`
					: 'This group has no members.',
			run: async () => {
				await api.deleteGroup(group.id);
				app.success(`Deleted ${group.name}`);
				await load();
			}
		};
	}

	function askBulkDelete() {
		const count = selection.size;
		confirming = {
			title: `Delete ${count} group${count === 1 ? '' : 's'}?`,
			message:
				'Members are not deleted: they leave the group and fall back to their own allowance and expiry.',
			run: async () => {
				await runBulk('delete');
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

	// A node shows its master's groups and changes none of them.

	function canModify(group: Group): boolean {
		if (!app.session || !canWrite) return false;
		return app.session.isAdmin || group.ownerId === app.session.peerId;
	}
</script>

<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
	<div>
		<h1 class="text-xl font-semibold">Groups</h1>
		<p class="text-xs" style="color: var(--text-muted)">
			A group's allowance and expiry apply to its members as a whole
		</p>
	</div>
	{#if canWrite}
		<button
			class="btn btn-primary"
			onclick={() => {
				editing = null;
				editorOpen = true;
			}}
		>
			<Icon name="plus" size={15} /> New group
		</button>
	{/if}
</div>

<div class="card mb-3 flex flex-wrap items-center gap-2 p-2.5">
	<div class="relative min-w-[200px] flex-1">
		<span class="absolute top-1/2 left-2.5 -translate-y-1/2" style="color: var(--text-faint)">
			<Icon name="search" size={14} />
		</span>
		<input
			class="field pl-8"
			bind:value={search}
			oninput={() => void load()}
			placeholder="Search groups"
			autocomplete="off"
		/>
	</div>
	<select class="field w-auto" bind:value={status} onchange={() => void load()} aria-label="Status">
		<option value="">Any status</option>
		<option value="active">Active</option>
		<option value="disabled">Disabled</option>
		<option value="expired">Expired</option>
		<option value="quota">Quota reached</option>
	</select>
</div>

{#if loadError}
	<div class="card mb-3 p-3 text-sm" style="border-color: var(--danger); color: var(--danger)">
		{loadError}
	</div>
{/if}

<div class="card overflow-hidden">
	<div class="overflow-x-auto">
		<table class="w-full min-w-[720px] text-sm">
			<thead
				class="text-left text-xs"
				style="background: var(--surface-2); color: var(--text-muted)"
			>
				<tr>
					<th class="w-9 px-3 py-2">
						<input
							type="checkbox"
							class="accent-[var(--primary)]"
							checked={allSelected}
							onchange={toggleAll}
							aria-label="Select all rows"
						/>
					</th>
					<SortHeader label="Name" field="name" {sort} {order} onsort={onSort} />
					<SortHeader label="Members" field="peers" {sort} {order} onsort={onSort} />
					<SortHeader label="Status" field="status" {sort} {order} onsort={onSort} />
					<SortHeader label="Usage" field="usage" {sort} {order} onsort={onSort} />
					<SortHeader label="Expires" field="expiry" {sort} {order} onsort={onSort} />
					{#if isAdmin}
						<th class="px-3 py-2 font-medium">Owner</th>
					{/if}
					<th class="w-px px-3 py-2"></th>
				</tr>
			</thead>
			<tbody>
				{#each groups as group (group.id)}
					<tr
						class="border-t transition-colors hover:bg-[var(--surface-2)]"
						style="border-color: var(--border)"
					>
						<td class="px-3 py-2">
							<input
								type="checkbox"
								class="accent-[var(--primary)]"
								checked={selection.has(group.id)}
								onchange={() => toggle(group.id)}
								aria-label="Select {group.name}"
							/>
						</td>
						<td class="px-3 py-2 font-medium whitespace-nowrap">
							{#if canModify(group)}
								<button
									class="cursor-pointer text-left font-medium hover:underline"
									onclick={() => {
										editing = group;
										editorOpen = true;
									}}
								>
									{group.name}
								</button>
							{:else}
								{group.name}
							{/if}
						</td>
						<td class="px-3 py-2">
							<a
								href="/peers?group={group.id}"
								class="text-xs hover:underline"
								style="color: var(--primary)"
							>
								{group.peerCount} peer{group.peerCount === 1 ? '' : 's'}
							</a>
						</td>
						<td class="px-3 py-2"><StatusTag status={group.status} /></td>
						<td class="px-3 py-2">
							<UsageBar used={group.usage} allowed={group.allowedUsage} />
						</td>
						<td class="px-3 py-2 text-xs whitespace-nowrap" style="color: var(--text-muted)">
							{relative(group.expiresAt)}
						</td>
						{#if isAdmin}
							<td class="px-3 py-2 text-xs" style="color: var(--text-muted)">
								{group.ownerName || '—'}
							</td>
						{/if}
						<td class="px-3 py-2">
							<div class="flex items-center justify-end gap-1">
								{#if canModify(group)}
									<button
										class="btn btn-default btn-sm"
										onclick={() => askResetGroup(group)}
										title="Reset the usage counted against this group"
									>
										<Icon name="refresh" size={13} /> Reset
									</button>
									<button
										class="btn btn-ghost btn-icon"
										onclick={() => {
											editing = group;
											editorOpen = true;
										}}
										title="Edit"
										aria-label="Edit {group.name}"
									>
										<Icon name="edit" size={15} />
									</button>
									<button
										class="btn btn-ghost btn-icon"
										style="color: var(--danger)"
										onclick={() => askDelete(group)}
										title="Delete"
										aria-label="Delete {group.name}"
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

	{#if groups.length === 0 && !firstLoad}
		<Empty
			title="No groups yet"
			hint="Groups share one allowance and expiry across several peers — useful for a household or a reseller's batch."
		/>
	{/if}
</div>

<BulkBar count={selection.size} onclear={() => (selection = new Set())}>
	<button class="btn btn-default btn-sm" onclick={() => runBulk('enable')} disabled={bulkBusy}>
		<Icon name="power" size={13} /> Enable
	</button>
	<button class="btn btn-default btn-sm" onclick={() => runBulk('disable')} disabled={bulkBusy}>
		<Icon name="power" size={13} /> Disable
	</button>
	<button class="btn btn-default btn-sm" onclick={() => runBulk('resetUsage')} disabled={bulkBusy}>
		<Icon name="refresh" size={13} /> Reset usage
	</button>
	<button class="btn btn-danger btn-sm" onclick={askBulkDelete} disabled={bulkBusy}>
		<Icon name="trash" size={13} /> Delete
	</button>
</BulkBar>

<GroupEditor
	open={editorOpen}
	group={editing}
	onclose={() => (editorOpen = false)}
	onsaved={() => void load()}
/>

<Confirm
	open={confirming !== null}
	title={confirming?.title ?? ''}
	message={confirming?.message ?? ''}
	confirmLabel={confirming?.confirmLabel ?? 'Delete'}
	danger={confirming?.danger ?? true}
	busy={confirmBusy}
	onconfirm={confirmRun}
	oncancel={() => (confirming = null)}
/>
