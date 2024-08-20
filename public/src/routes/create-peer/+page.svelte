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
			loading.set(true)
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

	async function createPeer() {
		try {
			name = name.trim()

			if (name === '') return (error = 'name can not be empty')

			if (userRole === 'admin' && !name.includes('-'))
				return (error = 'name should include at least one dash')

			loading.set(true)
			error = ''
			const res = await fetch('/api/peers', {
				method: 'POST',
				body: JSON.stringify({
					name: userRole === 'distributor' ? prefix + '-' + name : name,
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

<div class="flex min-h-[calc(100svh-96px)] items-center justify-center">
	<div class="flex w-full max-w-xl flex-col rounded border border-neutral-800 px-4 py-8">
		<div class="mb-4 text-center text-3xl font-bold">Create New Peer</div>
		<label for="name" class="mb-1">Peer Name</label>
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
		<div class="mb-4 hidden w-full">
			<input
				disabled={true}
				bind:value={preferredEndpoint}
				class="w-full rounded border border-neutral-800 bg-neutral-950 px-4 py-2 text-lg font-bold text-neutral-50 outline-none"
				type="text"
				placeholder="Preferred Endpoint"
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
		{#if userRole === 'admin'}
			<label for="role" class="mb-1">Role</label>
			<div class="mb-8 flex w-full">
				<select
					bind:value={role}
					class="w-full rounded border border-neutral-800 bg-neutral-900 px-4 py-2 text-lg font-bold outline-none"
				>
					<option value="user"> User </option>
					<option value="distributor"> Distributor </option>
					<option value="admin"> Admin </option>
				</select>
			</div>
		{/if}
		<div class="grid grid-cols-2 gap-2">
			<button
				on:click={createPeer}
				class="rounded bg-neutral-50 py-2 text-xl font-bold text-neutral-950 transition-colors hover:bg-neutral-300"
				>CREATE</button
			>
			<a
				href="/"
				class="rounded bg-neutral-50 py-2 text-center text-xl font-bold text-neutral-950 transition-colors hover:bg-neutral-300"
				>CANCEL</a
			>
		</div>

		{#if error}
			<div class="text-red-900">{error}</div>
		{/if}
	</div>
</div>
