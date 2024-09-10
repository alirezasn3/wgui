<script lang="ts">
	import { goto } from '$app/navigation'
	import { page } from '$app/stores'
	import { formatBytes, formatExpiry, type Group, type Peer } from '$lib'
	import { getContext, onMount } from 'svelte'
	import type { Writable } from 'svelte/store'
	const loading: Writable<boolean> = getContext('loading')
	const lastPageURL: Writable<URL | undefined> = getContext('lastPageURL')
	let groupID: string | null = null
	let group: null | Group = null
	let peers: Peer[] = []
	let error = ''
	let editing = false
	let expiresAtChanged = false
	let newName = ''
	let newAllowedUsage = 0
	let newExpiresAt = 0
	onMount(async () => {
		try {
			groupID = $page.url.searchParams.get('id')
			if (!groupID) return goto('/groups/all')
			const res = await fetch('/api/groups/' + groupID)
			if (res.status === 400 || res.status === 404) return goto('/groups/all')
			group = await res.json()
			if (!group) return
			const results = await Promise.allSettled(
				group.PeerIDs.map((id) => fetch('/api/peers/' + encodeURIComponent(id)))
			)
			let temp: Peer[] = []
			for (const r of results) {
				if (r.status === 'fulfilled') {
					const data: Peer = await r.value.json()
					temp.push(data)
				}
			}
			peers = temp
			loading.set(false)
		} catch (error) {
			console.log(error)
		}
	})

	async function deleteGroup() {
		try {
			error = ''

			if (!group) return

			if (!window.confirm(`Delete ${group.Name}?`)) return

			loading.set(true)

			const res = await fetch('/api/groups/' + group.ID, {
				method: 'DELETE'
			})
			if (res.status === 200) {
				await goto('/groups/all')
			} else {
				error = res.statusText
			}
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}

	async function deletePeerFromGroup(name: string, id: string) {
		try {
			error = ''

			if (!group) return

			if (!window.confirm(`Delete ${name} from ${group.Name}?`)) return

			loading.set(true)

			const res = await fetch(`/api/groups/${group.ID}/${encodeURIComponent(id)}`, {
				method: 'DELETE'
			})
			if (res.status === 200) location.reload()
			else error = res.statusText
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}

	async function resetGroupUsage() {
		try {
			error = ''

			if (!group) return

			loading.set(true)

			const res = await fetch('/api/groups/' + group.ID, {
				method: 'PUT'
			})

			if (res.status === 200) location.reload()
			else error = res.statusText
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}

	async function resetGroupExpiry() {
		try {
			error = ''

			if (!group) return

			loading.set(true)

			const res = await fetch('/api/groups/' + group.ID, {
				method: 'PATCH',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({
					expiresAt: Date.now() + 30 * 24 * 3600 * 1000
				})
			})

			if (res.status === 200) location.reload()
			else error = res.statusText
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}

	async function editGroup() {
		try {
			error = ''

			if (!group) return

			loading.set(true)

			const res = await fetch('/api/groups/' + group.ID, {
				method: 'PATCH',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({
					name: group.Name !== newName ? newName : undefined,
					allowedUsage:
						group.AllowedUsage / 1024000000 !== newAllowedUsage
							? newAllowedUsage * 1024000000
							: undefined,
					expiresAt: expiresAtChanged ? Date.now() + newExpiresAt * 24 * 3600 * 1000 : undefined
				})
			})
			if (res.status === 200) location.reload()
			else error = res.statusText
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}
</script>

{#if group}
	{#if error}
		<div class="mb-4 text-red-900">
			{error}
		</div>
	{/if}
	{#if editing}
		<div class="mb-2 w-full max-w-lg">
			<input
				bind:value={newName}
				class="w-full rounded border border-neutral-800 bg-neutral-950 px-4 py-2 text-neutral-50 outline-none"
				type="text"
				autocomplete="off"
				placeholder="Name"
			/>
		</div>
		<div class="mb-2 flex w-full max-w-lg">
			<input
				bind:value={newAllowedUsage}
				class="w-full rounded-l border-y border-l border-neutral-800 bg-neutral-950 px-4 py-2 text-neutral-50 outline-none"
				type="number"
				placeholder="Allowed Usage"
			/>
			<div
				class="flex items-center rounded-r border-y border-r border-neutral-800 bg-neutral-950 pr-2 text-neutral-50"
			>
				GB
			</div>
		</div>
		<div class="mb-2 flex w-full max-w-lg">
			<input
				on:change={() => (expiresAtChanged = true)}
				bind:value={newExpiresAt}
				class="w-full rounded-l border-y border-l border-neutral-800 bg-neutral-950 px-4 py-2 text-neutral-50 outline-none"
				type="number"
				placeholder="Expiry"
			/>
			<div
				class="flex items-center rounded-r border-y border-r border-neutral-800 bg-neutral-950 pr-2 text-neutral-50"
			>
				Days
			</div>
		</div>
		<div class="mb-4 grid w-full max-w-lg grid-cols-2 gap-2">
			<button
				on:click={editGroup}
				class="rounded bg-neutral-50 px-4 py-2 text-lg font-bold text-neutral-950 hover:bg-neutral-300"
				>SAVE</button
			>
			<button
				on:click={() => {
					editing = false
					error = ''
				}}
				class="rounded bg-neutral-50 px-4 py-2 text-lg font-bold text-neutral-950 hover:bg-neutral-300"
				>CANCEL</button
			>
		</div>
	{:else}
		{#if $lastPageURL?.pathname.includes('peer') && $lastPageURL.searchParams.get('id') !== null}
			<a
				href={'/peers?id=' + $lastPageURL.searchParams.get('id')}
				class="mb-4 block flex w-fit items-center rounded-full border border-neutral-800 p-2 pr-4 hover:cursor-pointer hover:bg-neutral-950"
			>
				<span class="material-symbols-outlined mr-2"> arrow_left </span>
				<span>Back to Peer</span>
			</a>
		{/if}
		<div class="mb-4 grid w-fit grid-cols-5 gap-2 md:grid-cols-9">
			<button on:click={deleteGroup}>
				<span
					class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
				>
					delete
				</span>
			</button>
			<button on:click={resetGroupUsage}>
				<span
					class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
				>
					restart_alt
				</span>
			</button>
			<button on:click={resetGroupExpiry} class="relative">
				<span
					class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
				>
					history
				</span>
				<div class="absolute right-0 top-0 text-xs font-thin">30</div>
			</button>
			<button
				on:click={() => {
					if (!group) return
					editing = true
					expiresAtChanged = false
					newName = group.Name
					newAllowedUsage = group.AllowedUsage / 1024000000
					newExpiresAt = +((group.ExpiresAt - Date.now()) / 1000 / 3600 / 24).toFixed(2)
				}}
			>
				<span
					class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
				>
					edit
				</span>
			</button>
		</div>
		<div class="mb-4">{group.Name}</div>
		<div class="mb-4">{formatExpiry(group.ExpiresAt)}</div>
		<div class="mb-4">
			{formatBytes(group.TotalRX + group.TotalTX)}/ {formatBytes(group.AllowedUsage)}
		</div>
		{#if peers.length}
			<div class="w-full max-w-full overflow-x-auto bg-neutral-950">
				<table class="w-full text-sm md:text-base">
					<thead class="bg-neutral-900 text-left">
						<th class="w-1 py-2"></th>
						<th class="px-2 py-2">#</th>
						<th class="px-2 py-2">Name</th>
						<th class="px-2 py-2">Expiry</th>
						<th class="px-2 py-2">Usage</th>
					</thead>
					<tbody>
						{#each peers as peer, i}
							<tr
								on:click={() => {
									goto('/peers?id=' + encodeURIComponent(peer.ID))
								}}
								class="{peer.Disabled && peer.TotalRX + peer.TotalTX >= peer.AllowedUsage
									? 'bg-yellow-700 hover:bg-yellow-800'
									: peer.Disabled
										? 'bg-red-800 hover:bg-red-900'
										: !peer.Disabled && peer.TotalRX + peer.TotalTX == 0
											? 'bg-blue-900 hover:bg-blue-800'
											: 'bg-neutral-900 hover:bg-neutral-800'} border-neutral-800 text-left odd:border-y hover:cursor-pointer"
							>
								<td
									class="flex items-center"
									on:click|stopPropagation={() => {
										deletePeerFromGroup(peer.Name, peer.ID)
									}}
								>
									<span class="material-symbols-outlined px-2 py-1 hover:text-red-900">
										delete
									</span>
								</td>
								<td class="px-2 py-1">{i + 1}</td>
								<td class="whitespace-nowrap px-2 py-1">{peer.Name}</td>
								<td class="whitespace-nowrap px-2 py-1">{formatExpiry(peer.ExpiresAt)}</td>
								<td class="whitespace-nowrap px-2 py-1"
									>{formatBytes(peer.TotalTX + peer.TotalRX)}/{formatBytes(peer.AllowedUsage)}</td
								>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	{/if}
{/if}
