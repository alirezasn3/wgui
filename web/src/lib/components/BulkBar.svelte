<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';

	interface Props {
		count: number;
		onclear: () => void;
		children: Snippet;
	}
	let { count, onclear, children }: Props = $props();
</script>

{#if count > 0}
	<div
		class="sticky bottom-4 z-20 mx-auto flex w-fit max-w-full flex-wrap items-center gap-2 rounded-lg border px-3 py-2 shadow-xl"
		style="background: var(--surface-3); border-color: var(--border-strong)"
	>
		<span class="px-1 text-sm font-medium whitespace-nowrap">
			{count} selected
		</span>
		<span class="h-5 w-px" style="background: var(--border-strong)"></span>
		{@render children()}
		<button class="btn btn-ghost btn-icon" onclick={onclear} aria-label="Clear selection">
			<Icon name="close" size={14} />
		</button>
	</div>
{/if}
