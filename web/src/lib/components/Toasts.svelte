<script lang="ts">
	import { app } from '$lib/app.svelte';
	import Icon from './Icon.svelte';

	const colors = {
		success: { bg: 'var(--success-soft)', fg: 'var(--success)' },
		error: { bg: 'var(--danger-soft)', fg: 'var(--danger)' },
		info: { bg: 'var(--info-soft)', fg: 'var(--info)' }
	};
</script>

<div
	class="pointer-events-none fixed right-4 bottom-4 z-[60] flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2"
>
	{#each app.toasts as toast (toast.id)}
		<div
			class="card pointer-events-auto flex items-start gap-2.5 p-3 text-sm shadow-lg"
			style="border-color: {colors[toast.kind].fg}40"
			role="status"
		>
			<span class="mt-0.5 shrink-0" style="color: {colors[toast.kind].fg}">
				<Icon name={toast.kind === 'success' ? 'check' : 'info'} />
			</span>
			<span class="min-w-0 flex-1 break-words">{toast.message}</span>
			<button
				class="btn btn-ghost btn-icon shrink-0"
				onclick={() => app.dismiss(toast.id)}
				aria-label="Dismiss"
			>
				<Icon name="close" size={14} />
			</button>
		</div>
	{/each}
</div>
