<script lang="ts">
	import { goto } from '$app/navigation'
	import { getContext, onMount } from 'svelte'
	import type { Writable } from 'svelte/store'

	const loading: Writable<boolean> = getContext('loading')

	let name = ''
	let allowedUsage = 30
	let expiresAt = 30
	let error = ''
	let userRole = 'user'
	let prefix = ''

	onMount(async () => {
		try {
			const res = await fetch('/api/me')
			const data = await res.json()
			userRole = data.role
			prefix = data.prefix
		} catch (e) {
			console.log(e)
		} finally {
			loading.set(false)
		}
	})

	async function createGroup() {
		try {
			name = name.trim()

			if (name === '') return (error = 'name can not be empty')

			loading.set(true)
			error = ''
			const res = await fetch('/api/groups', {
				method: 'POST',
				body: JSON.stringify({
					name: userRole === 'distributor' ? prefix + '-' + name : name,
					allowedUsage: allowedUsage * 1024000000,
					expiresAt: Date.now() + expiresAt * 24 * 3600 * 1000
				})
			})
			if (res.status === 201) {
				const id = await res.text()
				await goto('/groups/?id=' + id)
			} else error = await res.text()
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}
</script>

<div class="flex min-h-[calc(100svh-96px)] justify-center md:items-center">
	<div class="flex w-full max-w-xl flex-col rounded border-neutral-800 md:border md:px-4 md:py-8">
		<div class="mb-4 text-center text-3xl font-bold">Create New Group</div>
		<label for="name" class="mb-1">Group Name</label>
		<div class="mb-4 flex w-full">
			{#if userRole === 'distributor'}
				<span
					class="rounded-l border-y border-l border-neutral-800 bg-neutral-950 py-[6px] pl-2 pr-0.5 text-lg font-bold text-neutral-50"
					>{prefix}-</span
				>
			{/if}
			<input
				id="name"
				bind:value={name}
				class="{userRole === 'distributor'
					? 'rounded-r border-y border-r pr-2'
					: 'rounded border px-2'} w-full border-neutral-800 bg-neutral-950 py-1 text-lg font-bold text-neutral-50 outline-none"
				type="text"
				autocomplete="off"
				placeholder="Name"
			/>
		</div>
		<label for="allowed-usage" class="mb-1">Allowed Usage</label>
		<div class="mb-4 flex w-full">
			<input
				id="allowed-usage"
				bind:value={allowedUsage}
				class="w-full rounded-l border-y border-l border-neutral-800 bg-neutral-950 px-4 py-2 text-lg font-bold text-neutral-50 outline-none"
				type="number"
				placeholder="Allowed Usage"
			/>
			<div
				class="flex items-center rounded-r border-y border-r border-neutral-800 bg-neutral-950 pr-2 text-neutral-50"
			>
				GB
			</div>
		</div>
		<label for="expires-in" class="mb-1">Expires In</label>
		<div class="mb-4 flex w-full">
			<input
				id="expires-in"
				bind:value={expiresAt}
				class="w-full rounded-l border-y border-l border-neutral-800 bg-neutral-950 px-4 py-2 text-lg font-bold text-neutral-50 outline-none"
				type="number"
				placeholder="Expires In"
			/>
			<div
				class="flex items-center rounded-r border-y border-r border-neutral-800 bg-neutral-950 pr-2 text-neutral-50"
			>
				Days
			</div>
		</div>
		<div class="grid grid-cols-2 gap-2">
			<button
				on:click={createGroup}
				class="rounded bg-neutral-50 py-2 text-xl font-bold text-neutral-950 transition-colors hover:bg-neutral-300"
				>CREATE</button
			>
			<a
				href="/groups"
				class="rounded bg-neutral-50 py-2 text-center text-xl font-bold text-neutral-950 transition-colors hover:bg-neutral-300"
				>CANCEL</a
			>
		</div>
		{#if error}
			<div class="mt-4 text-red-900">{error}</div>
		{/if}
	</div>
</div>
