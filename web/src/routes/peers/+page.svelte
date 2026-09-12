<script lang="ts">
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { api, type PeerQuery } from '$lib/api';
	import { app, poll } from '$lib/app.svelte';
	import BulkBar from '$lib/components/BulkBar.svelte';
	import BulkEditor from '$lib/components/BulkEditor.svelte';
	import Confirm from '$lib/components/Confirm.svelte';
	import Empty from '$lib/components/Empty.svelte';
	import GroupEditor from '$lib/components/GroupEditor.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import Pagination from '$lib/components/Pagination.svelte';
	import PeerDetail from '$lib/components/PeerDetail.svelte';
	import PeerEditor from '$lib/components/PeerEditor.svelte';
	import SortHeader from '$lib/components/SortHeader.svelte';
	import StatusTag from '$lib/components/StatusTag.svelte';
	import UsageBar from '$lib/components/UsageBar.svelte';
	import { effectiveLimits, handshake, relative, speed } from '$lib/format';
	import type { Group, Peer, PeerBulkAction } from '$lib/types';
	import { onMount } from 'svelte';

	// -- query state, mirrored into the URL so views can be linked and reloaded
	let search = $state(page.url.searchParams.get('search') ?? '');
	let status = $state(page.url.searchParams.get('status') ?? '');
	let groupFilter = $state(Number(page.url.searchParams.get('group') ?? 0));
	let roleFilter = $state(page.url.searchParams.get('role') ?? '');
	let ownerFilter = $state(page.url.searchParams.get('owner') ?? '');
	let ownerFilterName = $state('');
	let sort = $state(page.url.searchParams.get('sort') ?? 'name');
	let order = $state<'asc' | 'desc'>(
		page.url.searchParams.get('order') === 'desc' ? 'desc' : 'asc'
	);
	let currentPage = $state(1);
	let pageSize = $state(50);

	let peers = $state<Peer[]>([]);
	let total = $state(0);
	let groups = $state<Group[]>([]);
	let distributors = $state<Peer[]>([]);
	let loadError = $state('');
	let firstLoad = $state(true);

	let selection = $state<Set<string>>(new Set());
	let editing = $state<Peer | null>(null);
	let editorOpen = $state(false);
	// Held by id, not by value: the row objects are replaced on every poll, so a
	// modal bound to the object it was opened with would freeze — stale usage, a
	// live rate that never moves.
	let detailId = $state<string | null>(null);
	let detailFetched = $state<Peer | null>(null);
	let detail = $derived(
		detailId
			? (peers.find((p) => p.id === detailId) ??
					(detailFetched?.id === detailId ? detailFetched : null))
			: null
	);
	let editingGroup = $state<Group | null>(null);
	let groupEditorOpen = $state(false);
	let bulkAction = $state<PeerBulkAction | null>(null);
	let bulkOpen = $state(false);
	let bulkBusy = $state(false);
	let confirming = $state<{ title: string; message: string; run: () => Promise<void> } | null>(
		null
	);
	let confirmBusy = $state(false);

	let canWrite = $derived(app.session?.canWrite ?? false);
	// A node shows everything and changes nothing: peers belong to its master,
	// so offering the controls would only lead to an edit that is refused.

	let isAdmin = $derived(app.session?.isAdmin ?? false);

	// Debounce the search box so typing does not fire a request per keystroke.
	let searchTimer: ReturnType<typeof setTimeout>;
	function onSearchInput(value: string) {
		search = value;
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			currentPage = 1;
			void load();
		}, 250);
	}

	function queryParams(): PeerQuery {
		return {
			search: search || undefined,
			status: status || undefined,
			group: groupFilter || undefined,
			role: roleFilter || undefined,
			owner: ownerFilter || undefined,
			sort,
			order,
			page: currentPage,
			pageSize
		};
	}

	function syncURL() {
		const params = new URLSearchParams();
		if (search) params.set('search', search);
		if (status) params.set('status', status);
		if (groupFilter) params.set('group', String(groupFilter));
		if (roleFilter) params.set('role', roleFilter);
		if (ownerFilter) params.set('owner', ownerFilter);
		if (sort !== 'name') params.set('sort', sort);
		if (order !== 'asc') params.set('order', order);
		const qs = params.toString();
		replaceState(qs ? `?${qs}` : page.url.pathname, {});
	}

	async function load() {
		try {
			const res = await api.peers(queryParams());
			peers = res.peers;
			total = res.total;
			// Drop selections that fell out of the current result set, so a bulk
			// action can never touch a row the operator can no longer see.
			const present = new Set(res.peers.map((p) => p.id));
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

	async function loadSupporting() {
		if (!canWrite) return;
		try {
			const [g, d] = await Promise.all([
				api.groups({ pageSize: 500, sort: 'name' }),
				isAdmin ? api.peers({ role: 'distributor', pageSize: 500, sort: 'name' }) : null
			]);
			groups = g.groups;
			distributors = d?.peers ?? [];
		} catch {
			/* the pickers simply stay empty */
		}
	}

	onMount(() => {
		void loadSupporting();

		// Deep link from the dashboard: ?open=<peer id>
		const open = page.url.searchParams.get('open');
		if (open) {
			detailId = open;
			// Deep links can point at a peer that is not on the current page, so
			// fetch it as a fallback for the list lookup.
			api
				.peer(open)
				.then((p) => (detailFetched = p))
				.catch(() => (detailId = null));
		}

		return poll(load, 1000);
	});

	function onSort(field: string) {
		if (sort === field) order = order === 'asc' ? 'desc' : 'asc';
		else {
			sort = field;
			order = 'asc';
		}
		syncURL();
		void load();
	}

	function applyFilters() {
		currentPage = 1;
		syncURL();
		void load();
	}

	function clearFilters() {
		search = '';
		status = '';
		groupFilter = 0;
		roleFilter = '';
		ownerFilter = '';
		ownerFilterName = '';
		applyFilters();
	}

	/** Narrows the list to everything one distributor (or admin) is responsible for. */
	function filterByOwner(peer: Peer) {
		if (!peer.ownerId) return;
		ownerFilter = peer.ownerId;
		ownerFilterName = peer.ownerName;
		applyFilters();
	}

	let hasFilters = $derived(!!search || !!status || !!groupFilter || !!roleFilter || !!ownerFilter);

	// -- selection
	let allSelected = $derived(peers.length > 0 && peers.every((p) => selection.has(p.id)));

	function toggleAll() {
		selection = allSelected ? new Set() : new Set(peers.map((p) => p.id));
	}

	function toggle(id: string) {
		const next = new Set(selection);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		selection = next;
	}

	// -- actions
	async function runBulk(action: PeerBulkAction, values: Record<string, unknown> = {}) {
		bulkBusy = true;
		try {
			const res = await api.bulkPeers({ ids: [...selection], action, ...values });
			app.success(`Updated ${res.affected} peer${res.affected === 1 ? '' : 's'}`);
			selection = new Set();
			bulkOpen = false;
			await load();
		} catch (e) {
			app.fail(e);
		} finally {
			bulkBusy = false;
		}
	}

	function askBulkDelete() {
		const count = selection.size;
		confirming = {
			title: `Delete ${count} peer${count === 1 ? '' : 's'}?`,
			message:
				'Their keys and tunnel addresses are released immediately and the devices using them stop connecting. This cannot be undone.',
			run: async () => {
				await runBulk('delete');
			}
		};
	}

	function askDelete(peer: Peer) {
		confirming = {
			title: `Delete ${peer.name}?`,
			message:
				'Its key and tunnel address are released immediately and the device using it stops connecting. This cannot be undone.',
			run: async () => {
				await api.deletePeer(peer.id);
				app.success(`Deleted ${peer.name}`);
				detailId = null;
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

	function openBulk(action: PeerBulkAction) {
		bulkAction = action;
		bulkOpen = true;
	}

	/** Opens a peer's group from the row or the detail dialog. */
	async function openGroup(id: number) {
		if (!id) return;
		// The picker's list is usually enough; fall back to fetching for a group
		// that did not fit in it.
		const known = groups.find((g) => g.id === id);
		try {
			editingGroup = known ?? (await api.group(id));
			groupEditorOpen = true;
		} catch (e) {
			app.fail(e);
		}
	}

	/** Three minutes, the window WireGuard itself treats a session as live for. */
	function isOnline(handshakeAt: number): boolean {
		return handshakeAt > 0 && Date.now() - handshakeAt < 180_000;
	}

	function canModify(peer: Peer): boolean {
		if (!app.session || !canWrite) return false;
		if (app.session.isAdmin) return true;
		return app.session.role === 'distributor' && peer.ownerId === app.session.peerId;
	}
</script>

<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
	<div>
		<h1 class="text-xl font-semibold">{canWrite ? 'Peers' : 'My connection'}</h1>
		<p class="text-xs" style="color: var(--text-muted)">
			{total} peer{total === 1 ? '' : 's'}{hasFilters ? ' matching the filters' : ''}
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
			<Icon name="plus" size={15} /> New peer
		</button>
	{/if}
</div>

{#if canWrite}
	<div class="card mb-3 flex flex-wrap items-center gap-2 p-2.5">
		<div class="relative min-w-[200px] flex-1">
			<span class="absolute top-1/2 left-2.5 -translate-y-1/2" style="color: var(--text-faint)">
				<Icon name="search" size={14} />
			</span>
			<input
				class="field pl-8"
				value={search}
				oninput={(e) => onSearchInput(e.currentTarget.value)}
				placeholder="Search name, address or public key"
				autocomplete="off"
			/>
		</div>

		<select class="field w-auto" bind:value={status} onchange={applyFilters} aria-label="Status">
			<option value="">Any status</option>
			<option value="active">Active</option>
			<option value="disabled">Disabled</option>
			<option value="expired">Expired</option>
			<option value="quota">Quota reached</option>
			<option value="online">Online anywhere</option>
			<option value="online-here">Online on this server</option>
			<option value="online-elsewhere">Online on another server</option>
			<option value="offline">Offline</option>
			<option value="never">Never connected</option>
		</select>

		<select
			class="field w-auto"
			bind:value={groupFilter}
			onchange={applyFilters}
			aria-label="Group"
		>
			<option value={0}>Any group</option>
			<option value={-1}>No group</option>
			{#each groups as group (group.id)}
				<option value={group.id}>{group.name}</option>
			{/each}
		</select>

		{#if isAdmin}
			<select
				class="field w-auto"
				bind:value={roleFilter}
				onchange={applyFilters}
				aria-label="Role"
			>
				<option value="">Any role</option>
				<option value="admin">Admins</option>
				<option value="distributor">Distributors</option>
				<option value="user">Users</option>
			</select>
		{/if}

		{#if ownerFilter}
			<button
				class="btn btn-default btn-sm"
				onclick={() => {
					ownerFilter = '';
					ownerFilterName = '';
					applyFilters();
				}}
				title="Stop filtering by owner"
			>
				Owner: {ownerFilterName || ownerFilter.slice(0, 8)}
				<Icon name="close" size={13} />
			</button>
		{/if}

		{#if hasFilters}
			<button class="btn btn-ghost btn-sm" onclick={clearFilters}>
				<Icon name="close" size={13} /> Clear
			</button>
		{/if}
	</div>
{/if}

{#if loadError}
	<div class="card mb-3 p-3 text-sm" style="border-color: var(--danger); color: var(--danger)">
		{loadError}
	</div>
{/if}

<div class="card overflow-hidden">
	<div class="overflow-x-auto">
		<table
			class="w-full min-w-[880px] table-fixed text-sm md:min-w-[1000px] lg:min-w-[1130px] xl:min-w-[1270px]"
		>
			<thead
				class="text-left text-xs"
				style="background: var(--surface-2); color: var(--text-muted)"
			>
				<tr>
					{#if canWrite}
						<th class="w-9 px-3 py-2">
							<input
								type="checkbox"
								class="accent-[var(--primary)]"
								checked={allSelected}
								onchange={toggleAll}
								aria-label="Select all rows"
							/>
						</th>
					{/if}
					<SortHeader label="Name" field="name" {sort} {order} onsort={onSort} />
					<SortHeader
						label="Address"
						field="ip"
						{sort}
						{order}
						onsort={onSort}
						class="w-[120px] max-md:hidden"
					/>
					<SortHeader
						label="Status"
						field="status"
						{sort}
						{order}
						onsort={onSort}
						class="w-[104px]"
					/>
					<SortHeader
						label="Usage"
						field="usage"
						{sort}
						{order}
						onsort={onSort}
						class="w-[196px]"
					/>
					<SortHeader
						label="Expires"
						field="expiry"
						{sort}
						{order}
						onsort={onSort}
						class="w-[120px]"
					/>
					<SortHeader
						label="Speed"
						field="speed"
						{sort}
						{order}
						onsort={onSort}
						align="right"
						class="w-[120px]"
					/>
					<SortHeader
						label="Handshake"
						field="handshake"
						{sort}
						{order}
						onsort={onSort}
						class="w-[140px] max-xl:hidden"
					/>
					{#if isAdmin}
						<th class="w-[130px] px-3 py-2 font-medium max-lg:hidden">Owner</th>
					{/if}
					<th class="w-[124px] px-3 py-2"></th>
				</tr>
			</thead>
			<tbody>
				{#each peers as peer (peer.id)}
					{@const limits = effectiveLimits(peer)}
					<tr
						class="border-t transition-colors hover:bg-[var(--surface-2)]"
						style="border-color: var(--border)"
					>
						{#if canWrite}
							<td class="px-3 py-2">
								<input
									type="checkbox"
									class="accent-[var(--primary)]"
									checked={selection.has(peer.id)}
									onchange={() => toggle(peer.id)}
									aria-label="Select {peer.name}"
								/>
							</td>
						{/if}
						<td class="px-3 py-2">
							<div class="flex items-center gap-1.5">
								<button
									class="min-w-0 flex-1 cursor-pointer truncate text-left font-medium hover:underline"
									onclick={() => (detailId = peer.id)}
									title={peer.name}
								>
									{peer.name}
								</button>
								{#if peer.sharedCount}
									<span
										class="flex shrink-0 items-center gap-0.5 rounded px-1 text-[10px]"
										style="background: var(--surface-3); color: var(--text-muted)"
										title="Can also see {peer.sharedCount} other peer{peer.sharedCount === 1
											? ''
											: 's'}"
									>
										<Icon name="share" size={9} />
										{peer.sharedCount}
									</span>
								{/if}
							</div>
							{#if peer.groupName}
								<button
									class="block max-w-full cursor-pointer truncate text-left text-[11px] hover:underline"
									style="color: var(--text-faint)"
									onclick={() => void openGroup(peer.groupId)}
									title="Open {peer.groupName}"
								>
									{peer.groupName}
								</button>
							{/if}
						</td>
						<td
							class="w-[120px] px-3 py-2 font-mono text-xs max-md:hidden"
							style="color: var(--text-muted)"
						>
							{peer.allowedIps}
						</td>
						<td class="w-[104px] px-3 py-2">
							<StatusTag status={peer.status} online={peer.online} />
							{#if !peer.online && peer.seenOn && isOnline(peer.lastHandshakeAt)}
								<span class="block truncate text-[10px]" style="color: var(--success)">
									on {peer.seenOn}
								</span>
							{/if}
						</td>
						<td class="w-[196px] px-3 py-2">
							<UsageBar
								used={limits.used}
								allowed={limits.allowed}
								shared={limits.shared}
								source={limits.usageFromGroup || undefined}
							/>
						</td>
						<td
							class="w-[120px] px-3 py-2 text-xs whitespace-nowrap tabular-nums"
							style="color: var(--text-muted)"
						>
							{relative(limits.expiresAt)}
							{#if limits.expiryFromGroup}
								<span class="block truncate text-[10px]" style="color: var(--text-faint)">
									via {limits.expiryFromGroup}
								</span>
							{/if}
						</td>
						<!-- Both lines are always drawn: an idle peer that renders a single
						     dash would change the row's height the moment traffic starts. -->
						<td
							class="w-[120px] px-3 py-2 text-right font-mono text-[11px] whitespace-nowrap tabular-nums"
						>
							<div style="color: {peer.txSpeed ? 'var(--success)' : 'var(--text-faint)'}">
								↓ {speed(peer.txSpeed)}
							</div>
							<div style="color: {peer.rxSpeed ? 'var(--info)' : 'var(--text-faint)'}">
								↑ {speed(peer.rxSpeed)}
							</div>
						</td>
						<td
							class="w-[140px] px-3 py-2 text-xs whitespace-nowrap tabular-nums max-xl:hidden"
							style="color: var(--text-muted)"
						>
							{handshake(peer.lastHandshakeAt)}
							{#if peer.seenOn}
								<span class="block truncate text-[10px]" style="color: var(--text-faint)">
									on {peer.seenOn}
								</span>
							{/if}
						</td>
						{#if isAdmin}
							<td class="w-[130px] px-3 py-2 text-xs max-lg:hidden">
								{#if peer.ownerName}
									<button
										class="cursor-pointer truncate hover:underline"
										style="color: var(--text-muted)"
										onclick={() => filterByOwner(peer)}
										title="Show everything {peer.ownerName} owns"
									>
										{peer.ownerName}
									</button>
								{:else}
									<span style="color: var(--text-faint)">—</span>
								{/if}
							</td>
						{/if}
						<td class="w-[124px] px-3 py-2">
							<div class="flex items-center justify-end gap-1">
								<button
									class="btn btn-ghost btn-icon"
									onclick={() => (detailId = peer.id)}
									title="Configuration and QR code"
									aria-label="Configuration for {peer.name}"
								>
									<Icon name="qr" size={15} />
								</button>
								{#if canModify(peer)}
									<button
										class="btn btn-ghost btn-icon"
										onclick={() => {
											editing = peer;
											editorOpen = true;
										}}
										title="Edit"
										aria-label="Edit {peer.name}"
									>
										<Icon name="edit" size={15} />
									</button>
									<button
										class="btn btn-ghost btn-icon"
										style="color: var(--danger)"
										onclick={() => askDelete(peer)}
										title="Delete"
										aria-label="Delete {peer.name}"
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

	{#if peers.length === 0 && !firstLoad}
		<Empty
			title={hasFilters ? 'No peers match these filters' : 'No peers yet'}
			hint={hasFilters
				? 'Try clearing the search or picking a different status.'
				: canWrite
					? 'Create the first one with the button above.'
					: undefined}
		/>
	{/if}

	<div class="border-t" style="border-color: var(--border)">
		<Pagination
			page={currentPage}
			{pageSize}
			{total}
			onpage={(p) => {
				currentPage = p;
				void load();
			}}
			onpageSize={(s) => {
				pageSize = s;
				currentPage = 1;
				void load();
			}}
		/>
	</div>
</div>

{#if canWrite}
	<BulkBar count={selection.size} onclear={() => (selection = new Set())}>
		<button class="btn btn-default btn-sm" onclick={() => runBulk('enable')} disabled={bulkBusy}>
			<Icon name="power" size={13} /> Enable
		</button>
		<button class="btn btn-default btn-sm" onclick={() => runBulk('disable')} disabled={bulkBusy}>
			<Icon name="power" size={13} /> Disable
		</button>
		<button
			class="btn btn-default btn-sm"
			onclick={() => openBulk('setExpiry')}
			disabled={bulkBusy}
		>
			Set expiry
		</button>
		<button class="btn btn-default btn-sm" onclick={() => openBulk('setUsage')} disabled={bulkBusy}>
			Set allowance
		</button>
		<button class="btn btn-default btn-sm" onclick={() => openBulk('setGroup')} disabled={bulkBusy}>
			Group
		</button>
		{#if isAdmin}
			<button
				class="btn btn-default btn-sm"
				onclick={() => openBulk('setRole')}
				disabled={bulkBusy}
			>
				Role
			</button>
		{/if}
		<button
			class="btn btn-default btn-sm"
			onclick={() => runBulk('resetUsage')}
			disabled={bulkBusy}
		>
			<Icon name="refresh" size={13} /> Reset usage
		</button>
		<button class="btn btn-danger btn-sm" onclick={askBulkDelete} disabled={bulkBusy}>
			<Icon name="trash" size={13} /> Delete
		</button>
	</BulkBar>
{/if}

<PeerEditor
	open={editorOpen}
	peer={editing}
	{groups}
	{distributors}
	onclose={() => (editorOpen = false)}
	onsaved={(saved) => {
		// A peer is only useful once its configuration is in someone's hands, so
		// a newly created one opens straight into the dialog that hands it over.
		// It is kept as the fallback too: it may not be on the page the list is
		// showing, which is sorted and paged without regard for what is new.
		if (!editing) {
			detailFetched = saved;
			detailId = saved.id;
		}
		void load();
	}}
/>

<PeerDetail
	open={detail !== null}
	peer={detail}
	canModify={detail ? canModify(detail) : false}
	onclose={() => (detailId = null)}
	onedit={(peer) => {
		detailId = null;
		editing = peer;
		editorOpen = true;
	}}
	ongroup={(id) => {
		detailId = null;
		void openGroup(id);
	}}
	onchanged={() => void load()}
/>

<GroupEditor
	open={groupEditorOpen}
	group={editingGroup}
	onclose={() => (groupEditorOpen = false)}
	onsaved={() => {
		void loadSupporting();
		void load();
	}}
/>

<BulkEditor
	open={bulkOpen}
	action={bulkAction}
	count={selection.size}
	{groups}
	canSetRole={isAdmin}
	busy={bulkBusy}
	onclose={() => (bulkOpen = false)}
	onapply={(values) => {
		if (!bulkAction) return;
		const payload: Record<string, unknown> = {};
		if (bulkAction === 'setUsage') payload.allowedUsage = values.allowedUsage;
		if (bulkAction === 'setExpiry') payload.expiryDays = values.expiryDays;
		if (bulkAction === 'setGroup') payload.groupId = values.groupId;
		if (bulkAction === 'setRole') payload.role = values.role;
		void runBulk(bulkAction, payload);
	}}
/>

<Confirm
	open={confirming !== null}
	title={confirming?.title ?? ''}
	message={confirming?.message ?? ''}
	confirmLabel="Delete"
	danger
	busy={confirmBusy}
	onconfirm={confirmRun}
	oncancel={() => (confirming = null)}
/>
