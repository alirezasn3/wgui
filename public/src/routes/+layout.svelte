<script lang="ts">
	import { beforeNavigate } from '$app/navigation'
	import '../app.css'
	import { onMount, setContext } from 'svelte'
	import { writable } from 'svelte/store'
	import { fade, fly } from 'svelte/transition'

	export let data

	const loading = writable(true)
	const search = writable('')
	const role = writable('user')
	const lastPageURL = writable<URL | undefined>(undefined)
	let showMenu = false

	beforeNavigate(({ from }) => {
		lastPageURL.set(from?.url)
		showMenu = false
		loading.set(true)
	})

	onMount(async () => {
		try {
			const res = await fetch('/api/me')
			const data = await res.json()
			role.set(data.role)
		} catch (error) {
			console.log(error)
		}
	})

	setContext('loading', loading)
	setContext('search', search)
	setContext('role', role)
	setContext('lastPageURL', lastPageURL)
</script>

<div class="min-h-svh w-full bg-neutral-950 text-neutral-50">
	{#if showMenu}
		<div
			transition:fly={{ x: 25 }}
			class="fixed left-0 top-0 z-20 flex h-svh w-full flex-col items-center justify-center bg-neutral-900 p-4 pt-12 text-xl font-bold"
		>
			<button on:click={() => (showMenu = false)} class="absolute right-0 top-0 p-4">
				<span class="material-symbols-outlined"> close </span>
			</button>
			<a href="/peers/all" class="my-2 w-40 rounded bg-neutral-50 py-2 text-center text-neutral-950"
				>PEERS</a
			>
			<a
				href="/groups/all"
				class="my-2 w-40 rounded bg-neutral-50 py-2 text-center text-neutral-950">GROUPS</a
			>
			<a href="/logs" class="my-2 w-40 rounded bg-neutral-50 py-2 text-center text-neutral-950"
				>LOGS</a
			>
		</div>
	{/if}
	{#if $role !== 'user'}<nav
			class="flex h-16 items-center justify-between border-b border-neutral-800 bg-neutral-950 px-4"
		>
			<div class="text-lg font-bold">WGUI</div>
			<div class="max-md:hidden">
				<a
					href="/peers/all"
					class="rounded bg-neutral-50 px-4 py-2 font-semibold text-neutral-950 transition-colors hover:bg-neutral-300"
					>PEERS</a
				>
				<a
					href="/groups/all"
					class="rounded bg-neutral-50 px-4 py-2 font-semibold text-neutral-950 transition-colors hover:bg-neutral-300"
					>GROUPS</a
				>
				<a
					href="/logs"
					class="rounded bg-neutral-50 px-4 py-2 font-semibold text-neutral-950 transition-colors hover:bg-neutral-300"
					>LOGS</a
				>
			</div>
			<button on:click={() => (showMenu = true)} class="relative md:hidden">
				<span class="material-symbols-outlined"> menu_open </span>
			</button>
		</nav>
	{/if}
	{#key data.url}
		<div
			in:fade={{ duration: 500, delay: 500 }}
			out:fade={{ duration: 500 }}
			class="min-h-[calc(100svh-64px)] p-4"
		>
			<slot />
		</div>
	{/key}
	{#if $loading}
		<div
			class="fixed left-0 top-0 flex h-full w-full items-center justify-center bg-neutral-950 bg-opacity-80"
		>
			<svg
				class="h-12 w-12 animate-spin text-neutral-50"
				xmlns="http://www.w3.org/2000/svg"
				fill="none"
				viewBox="0 0 24 24"
			>
				<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"
				></circle>
				<path
					class="opacity-75"
					fill="currentColor"
					d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
				></path>
			</svg>
		</div>
	{/if}
</div>
