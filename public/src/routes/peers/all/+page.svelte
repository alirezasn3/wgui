<script lang="ts">
	import { formatBytes, formatExpiry, sleep, type Peer } from '$lib'
	import { getContext, onMount } from 'svelte'
	import type { Writable } from 'svelte/store'
	import { goto } from '$app/navigation'

	const loading: Writable<boolean> = getContext('loading')
	const search: Writable<string> = getContext('search')
	const role: Writable<string> = getContext('role')

	let peers: Peer[] = []
	let combinedUsage = ''
	let err = ''

	$: combinedUsage = formatBytes(
		peers.reduce((previous: number, current: Peer) => {
			if (
				current.name.toLowerCase().includes($search.toLocaleLowerCase()) ||
				current.allowedIPs.includes($search)
			)
				return previous + current.totalRX + current.totalTX
			return previous
		}, 0)
	)

	onMount(async () => {
		try {
			let data
			while (true) {
				if ($loading) loading.set(false)
				const res = await fetch('/api/peers')
				if (res.status === 200) {
					data = await res.json()
					// @ts-ignore
					peers = (data.peers as Peer[]).sort((a, b) => a.expiresAt - b.expiresAt)
					// @ts-ignore
					$role = data.role
				} else {
					console.log(res.statusText)
				}
				await sleep(1000)
			}
		} catch (error) {
			err = (error as Error).message
			console.log(error)
		}
	})
</script>

{#if err}
	<div>
		{err}
	</div>
{/if}
{#if $role !== 'user'}
	<div class="relative mb-2 flex items-center">
		<input
			class="w-full rounded border border-neutral-800 bg-neutral-950 px-4 py-2 text-lg outline-none"
			bind:value={$search}
			placeholder="Search Peers"
			type="text"
			autocomplete="off"
		/>
		{#if $search.length}
			<button on:click={() => ($search = '')} class="absolute right-2 flex items-center">
				<span class="material-symbols-outlined"> close </span>
			</button>
		{/if}
	</div>
	{#if $search.length === 0}
		<div
			class="mb-2 flex items-center rounded border border-neutral-800 px-4 py-2 text-xs md:text-sm"
		>
			<div class="flex items-center">
				<div class="mr-2 h-2 w-2 rounded-full bg-blue-900"></div>
				No Usage
			</div>
			<div class="mx-4 flex items-center">
				<div class="mr-2 h-2 w-2 rounded-full bg-red-800"></div>
				Expired
			</div>
			<div class="flex items-center">
				<div class="mr-2 h-2 w-2 rounded-full bg-yellow-700"></div>
				Limited
			</div>
		</div>
	{/if}
	{#if $search.length > 0}
		<div class="mb-2 rounded border border-neutral-800 px-4 py-2">
			Combined Usage: {combinedUsage}
		</div>
	{/if}
{/if}

<div class="w-full max-w-full overflow-x-auto bg-neutral-950">
	{#if $search.length === 0 && !$loading && $role != 'user'}
		<a
			href="/peers/create"
			class="mb-4 block w-fit rounded bg-neutral-50 px-4 py-2 text-neutral-950">NEW PEER</a
		>
	{/if}
	<table class="w-full text-sm md:text-base">
		<thead class="bg-neutral-900 text-left">
			<th class="px-2 py-2">#</th>
			<th class="px-2 py-2">Name</th>
			<th class="px-2 py-2">Expiry</th>
			<th class="px-2 py-2">Usage</th>
		</thead>
		<tbody>
			{#each peers.filter((p) => !search || p.name
						.toLowerCase()
						.includes($search.toLowerCase()) || p.allowedIPs.includes($search)) as peer, i}
				<tr
					on:click={() => {
						goto('/peers?id=' + encodeURIComponent(peer.ID))
					}}
					class="{peer.disabled && peer.totalRX + peer.totalTX >= peer.allowedUsage
						? 'bg-yellow-700 hover:bg-yellow-800'
						: peer.disabled
							? 'bg-red-800 hover:bg-red-900'
							: !peer.disabled && peer.totalRX + peer.totalTX == 0
								? 'bg-blue-900 hover:bg-blue-800'
								: 'bg-neutral-900 hover:bg-neutral-800'} border-neutral-800 text-left odd:border-y hover:cursor-pointer"
				>
					<td class="px-2 py-1">{i + 1}</td>
					<td class="whitespace-nowrap px-2 py-1">{peer.name}</td>
					<td class="whitespace-nowrap px-2 py-1">{formatExpiry(peer.expiresAt)}</td>
					<td class="whitespace-nowrap px-2 py-1"
						>{formatBytes(peer.totalTX + peer.totalRX)}/{formatBytes(peer.allowedUsage)}</td
					>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
