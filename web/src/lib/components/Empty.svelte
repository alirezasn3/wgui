<script lang="ts">
	interface Props {
		title: string;
		hint?: string;
	}
	let { title, hint }: Props = $props();
</script>

<div class="flex flex-col items-center justify-center gap-1 px-4 py-14 text-center">
	<p class="text-sm font-medium">{title}</p>
	{#if hint}
		<p class="max-w-sm text-xs" style="color: var(--text-muted)">{hint}</p>
	{/if}
</div>
