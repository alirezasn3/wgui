<script lang="ts">
	import { getContext, onMount } from 'svelte'
	import type { Writable } from 'svelte/store'

	const loading: Writable<boolean> = getContext('loading')

	let name = ''
	let allowedUsage = 30
	let expiresAt = 30
	let preferredEndpoint = ''
	let error = ''
	let role = 'user'
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
		}
	})

	async function createPeer() {
		try {
			loading.set(true)
			error = ''
			const res = await fetch('/api/peers', {
				method: 'POST',
				body: JSON.stringify({
					name: userRole === 'distributor' ? prefix + name : name,
					allowedUsage: allowedUsage * 1024000000,
					expiresAt: Date.now() + expiresAt * 24 * 3600 * 1000,
					preferredEndpoint: preferredEndpoint.length ? preferredEndpoint : undefined,
					role
				})
			})
			if (res.status === 201) {
				const id = await res.text()
				location.assign('/?peer=' + encodeURIComponent(id))
			} else error = res.statusText
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}
</script>

<div class="-my-4 flex min-h-svh w-full items-center justify-center">
	<div
		class="flex w-full max-w-md flex-col items-center justify-center rounded border border-neutral-800 p-4"
	>
		<div class="mb-4 text-3xl font-bold">Create New Peer</div>
		<div class="mb-2 flex w-full">
			{#if userRole === 'distributor'}
				<span
					class="rounded-l border-y border-l border-neutral-800 bg-neutral-950 py-[6px] pl-2 pr-0.5 text-lg font-bold text-neutral-50"
					>{prefix}</span
				>
			{/if}
			<input
				bind:value={name}
				class="{userRole === 'distributor'
					? 'rounded-r border-y border-r pr-2'
					: 'rounded border px-2'} w-full border-neutral-800 bg-neutral-950 py-1 text-lg font-bold text-neutral-50 outline-none"
				type="text"
				autocomplete="off"
				placeholder="Name"
			/>
		</div>
		<div class="mb-2 w-full hidden">
			<input
				disabled={true}
				bind:value={preferredEndpoint}
				class="w-full rounded border border-neutral-800 bg-neutral-950 px-2 py-1 text-lg font-bold text-neutral-50 outline-none"
				type="text"
				placeholder="Preferred Endpoint"
			/>
		</div>
		<div class="mb-2 flex w-full">
			<input
				bind:value={allowedUsage}
				class="w-full rounded-l border-y border-l border-neutral-800 bg-neutral-950 px-2 py-1 text-lg font-bold text-neutral-50 outline-none"
				type="number"
				placeholder="Allowed Usage"
			/>
			<div
				class="flex items-center rounded-r border-y border-r border-neutral-800 bg-neutral-950 pr-2 text-neutral-50"
			>
				GB
			</div>
		</div>
		<div class="mb-2 flex w-full">
			<input
				bind:value={expiresAt}
				class="w-full rounded-l border-y border-l border-neutral-800 bg-neutral-950 px-2 py-1 text-lg font-bold text-neutral-50 outline-none"
				type="number"
				placeholder="Expiry"
			/>
			<div
				class="flex items-center rounded-r border-y border-r border-neutral-800 bg-neutral-950 pr-2 text-neutral-50"
			>
				Days
			</div>
		</div>
		{#if userRole === 'admin'}
			<div class="mb-2 flex w-full">
				<select
					bind:value={role}
					class="w-full rounded border border-neutral-800 bg-neutral-900 px-2 py-1 text-lg font-bold outline-none"
				>
					<option value="user"> User </option>
					<option value="distributor"> Distributor </option>
					<option value="admin"> Admin </option>
				</select>
			</div>
		{/if}
		<div class="mb-2 flex w-full justify-between">
			<button
				on:click={createPeer}
				class="w-[45%] rounded border border-neutral-800 px-2 py-1.5 text-lg font-bold"
				>Create</button
			>
			<button
				on:click={() => location.assign('/')}
				class="w-[45%] rounded border border-neutral-800 px-2 py-1.5 text-lg font-bold"
				>Cancel</button
			>
		</div>

		{#if error}
			<div class="text-red-900">{error}</div>
		{/if}
	</div>
</div>
