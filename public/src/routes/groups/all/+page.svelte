<script lang="ts">
	import { goto } from '$app/navigation'
	import { formatBytes, formatExpiry, type Group } from '$lib'
	import { getContext, onMount } from 'svelte'
	import type { Writable } from 'svelte/store'
	const loading: Writable<boolean> = getContext('loading')
	let groups: Group[] = []
	onMount(async () => {
		try {
			const res = await fetch('/api/groups')
			groups = await res.json()
		} catch (error) {
			console.log(error)
		} finally {
			loading.set(false)
		}
	})
</script>

<div class="w-full max-w-full overflow-x-auto bg-neutral-950">
	<a href="/groups/create" class="mb-4 block w-fit rounded bg-neutral-50 px-4 py-2 text-neutral-950"
		>NEW GROUP</a
	>
	<table class="w-full text-sm md:text-base">
		<thead class="bg-neutral-900 text-left">
			<th class="px-2 py-2">#</th>
			<th class="px-2 py-2">Name</th>
			<th class="px-2 py-2">Peers</th>
			<th class="px-2 py-2">Expiry</th>
			<th class="px-2 py-2">Usage</th>
		</thead>
		<tbody>
			{#each groups as group, i}
				<tr
					on:click={() => goto('/groups?id=' + encodeURIComponent(group.ID))}
					class="{group.Disabled && group.TotalRX + group.TotalTX >= group.AllowedUsage
						? 'bg-yellow-700 hover:bg-yellow-800'
						: group.Disabled
							? 'bg-red-800 hover:bg-red-900'
							: !group.Disabled && group.TotalRX + group.TotalTX === 0
								? 'bg-blue-900 hover:bg-blue-800'
								: 'bg-neutral-900 hover:bg-neutral-800'} border-neutral-800 text-left odd:border-y hover:cursor-pointer"
				>
					<td class="px-2 py-1">{i + 1}</td>
					<td class="whitespace-nowrap px-2 py-1">{group.Name}</td>
					<td class="whitespace-nowrap px-2 py-1">{group.PeerIDs.length}</td>
					<td class="whitespace-nowrap px-2 py-1">{formatExpiry(group.ExpiresAt)}</td>
					<td class="whitespace-nowrap px-2 py-1"
						>{formatBytes(group.TotalTX + group.TotalRX)}/{formatBytes(group.AllowedUsage)}</td
					>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
