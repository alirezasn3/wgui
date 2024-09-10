<script lang="ts">
	import { formatBytes, formatExpiry, sleep, type Peer } from '$lib'
	import { onMount } from 'svelte'
	import { getContext } from 'svelte'
	import type { Writable } from 'svelte/store'
	import protobuf, { Message } from 'protobufjs'
	import { goto } from '$app/navigation'

	const loading: Writable<boolean> = getContext('loading')
	const search: Writable<string> = getContext('search')
	const role: Writable<string> = getContext('role')

	let peers: Peer[] = []
	let combinedUsage = ''
	let shouldUpdatePeers = true

	$: combinedUsage = formatBytes(
		peers.reduce((previous: number, current: Peer) => {
			if (
				current.Name.toLowerCase().includes($search.toLocaleLowerCase()) ||
				current.AllowedIPs.includes($search)
			)
				return previous + current.TotalRX + current.TotalTX
			return previous
		}, 0)
	)

	async function updatePeers(PBPeers: protobuf.Type) {
		try {
			let ab
			let data
			while (shouldUpdatePeers) {
				if ($loading) loading.set(false)
				const res = await fetch('/api/peers')
				if (res.status === 200) {
					ab = await res.arrayBuffer()
					data = PBPeers.decode(new Uint8Array(ab), ab.byteLength)
					// @ts-ignore
					peers = (data.Peers as Peer[]).sort((a, b) => a.ExpiresAt - b.ExpiresAt)
					// @ts-ignore
					$role = data.Role
				} else {
					console.log(res.statusText)
				}
				await sleep(1000)
			}
		} catch (error) {
			console.log(error)
		}
	}

	onMount(() => {
		try {
			protobuf
				.load('/Peer.proto')
				.then((pb) => pb.lookupType('PBPeers'))
				.then(updatePeers)
				.catch(console.log)

			return () => {
				shouldUpdatePeers = false
			}
		} catch (e) {
			console.log(e)
		}
	})
</script>

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
		</div>{/if}
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
			{#each peers.filter((p) => !search || p.Name.toLowerCase().includes($search.toLowerCase()) || p.AllowedIPs.includes($search)) as peer, i}
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
