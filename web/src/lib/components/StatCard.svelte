<script lang="ts">
	import Icon from './Icon.svelte';

	interface Props {
		label: string;
		value: string;
		hint?: string;
		icon?: Parameters<typeof Icon>[0] extends never ? never : any;
		accent?: string;
	}
	let { label, value, hint, icon, accent = 'var(--primary)' }: Props = $props();
</script>

<div class="card flex items-center gap-3.5 p-4">
	{#if icon}
		<div
			class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg"
			style="background: color-mix(in srgb, {accent} 14%, transparent); color: {accent}"
		>
			<Icon name={icon} size={18} />
		</div>
	{/if}
	<div class="min-w-0">
		<div class="truncate text-xs" style="color: var(--text-muted)">{label}</div>
		<div class="truncate text-lg leading-tight font-semibold">{value}</div>
		{#if hint}
			<div class="truncate text-[11px]" style="color: var(--text-faint)">{hint}</div>
		{/if}
	</div>
</div>
