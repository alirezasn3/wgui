<script lang="ts">
	import type { Log } from '$lib'
	import { onMount } from 'svelte'
	import { getContext } from 'svelte'
	import type { Writable } from 'svelte/store'

	const loading: Writable<boolean> = getContext('loading')

	let error = ''
	let logs: Log[] = []

	onMount(async () => {
		try {
			loading.set(true)
			const res = await fetch('/api/logs')
			if (res.status !== 200) {
				error = res.statusText
			} else {
				logs = await res.json()
				logs = logs.reverse()
			}
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	})
</script>

{#each logs as log}
	<div class="my-1 flex flex-col rounded border border-neutral-800 px-2 py-1 text-sm md:text-base">
		<div
			class="font-bold {log.level === 'ERROR'
				? 'text-red-500'
				: log.level === 'WARN'
					? 'text-yellow-500'
					: 'text-neutral-50'}"
		>
			<div>{log.msg}</div>
		</div>
		{#if log.peer}
			<div class="text-sm text-neutral-300">{log.peer}</div>
		{/if}
		<div class="flex items-center text-xs text-neutral-300">
			<div>
				{new Date(log.time)
					.toLocaleTimeString('en-US', {
						year: 'numeric',
						month: 'numeric',
						day: 'numeric'
					})
					.replace(' ', '')}
			</div>
			<div class="mx-1 h-1 w-1 rounded-full bg-neutral-800"></div>
			<div class="text-neutral-300">{log.publicAddress}</div>
		</div>
	</div>
{/each}
