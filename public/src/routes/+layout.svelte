<script>
	import '../app.css'
	import { setContext } from 'svelte'
	import { writable } from 'svelte/store'

	const loading = writable(false)

	setContext('loading', loading)
</script>

<div class="min-h-svh w-full bg-neutral-950 p-4 text-neutral-50">
	<slot />
	{#if $loading}
		<div
			class="absolute left-0 top-0 flex h-full w-full items-center justify-center bg-neutral-950 bg-opacity-80"
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
