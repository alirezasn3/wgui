<script lang="ts">
	import { statusLabel } from '$lib/format';
	import type { Status } from '$lib/types';

	interface Props {
		status: Status | string;
		online?: boolean;
	}
	let { status, online = false }: Props = $props();

	// A peer can be active but idle, so "online" is shown as a separate dot
	// rather than folded into the status itself.
	const colors: Record<string, { bg: string; fg: string }> = {
		active: { bg: 'var(--success-soft)', fg: 'var(--success)' },
		disabled: { bg: 'var(--surface-3)', fg: 'var(--text-muted)' },
		expired: { bg: 'var(--danger-soft)', fg: 'var(--danger)' },
		quota: { bg: 'var(--warning-soft)', fg: 'var(--warning)' }
	};
	let color = $derived(colors[status] ?? colors.disabled);
</script>

<span class="tag" style="background: {color.bg}; color: {color.fg}">
	{#if status === 'active'}
		<span
			class="inline-block h-1.5 w-1.5 rounded-full"
			style="background: {online ? 'var(--success)' : 'var(--text-faint)'}"
			title={online ? 'Handshaked in the last 3 minutes' : 'No recent handshake'}
		></span>
	{/if}
	{statusLabel(status)}
</span>
