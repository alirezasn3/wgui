<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';

	interface Props {
		open: boolean;
		title: string;
		subtitle?: string;
		width?: string;
		onclose: () => void;
		children: Snippet;
		footer?: Snippet;
	}
	let { open, title, subtitle, width = '32rem', onclose, children, footer }: Props = $props();

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') onclose();
	}
</script>

<svelte:window on:keydown={open ? onkeydown : undefined} />

{#if open}
	<div class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 sm:p-8">
		<!-- The backdrop closes the dialog, matching the rest of the panel. -->
		<div
			class="fixed inset-0 bg-black/60 backdrop-blur-[2px]"
			role="button"
			tabindex="-1"
			aria-label="Close"
			onclick={onclose}
			onkeydown={(e) => e.key === 'Enter' && onclose()}
		></div>

		<div
			class="card relative z-10 my-auto w-full shadow-2xl"
			style="max-width: {width}"
			role="dialog"
			aria-modal="true"
			aria-label={title}
		>
			<header
				class="flex items-start justify-between gap-4 border-b px-5 py-3.5"
				style="border-color: var(--border)"
			>
				<div class="min-w-0">
					<h2 class="truncate text-base font-semibold">{title}</h2>
					{#if subtitle}
						<p class="mt-0.5 truncate text-xs" style="color: var(--text-muted)">{subtitle}</p>
					{/if}
				</div>
				<button class="btn btn-ghost btn-icon shrink-0" onclick={onclose} aria-label="Close">
					<Icon name="close" />
				</button>
			</header>

			<div class="px-5 py-4">
				{@render children()}
			</div>

			{#if footer}
				<footer
					class="flex justify-end gap-2 border-t px-5 py-3"
					style="border-color: var(--border); background: var(--surface-2)"
				>
					{@render footer()}
				</footer>
			{/if}
		</div>
	</div>
{/if}
