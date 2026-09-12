<script lang="ts">
	import { bytes, isUnlimited, usagePercent } from '$lib/format';

	interface Props {
		/** What this peer (or group) has consumed. */
		used: number;
		allowed: number;
		/**
		 * Everything consumed against the same allowance when it is shared with a
		 * group. `used` is then this peer's share of it, and the difference is
		 * drawn behind the bar as the rest of the group's spending.
		 */
		shared?: number;
		/** Named when the allowance comes from a group rather than the peer. */
		source?: string;
		/** Hides the second line where space is tight. */
		compact?: boolean;
	}
	let { used, allowed, shared = 0, source, compact = false }: Props = $props();

	// The allowance is depleted by everything charged to it, so what is left and
	// how alarming the bar looks both follow the shared total when there is one.
	let spent = $derived(Math.max(shared, used));
	let percent = $derived(usagePercent(used, allowed));
	let spentPercent = $derived(usagePercent(spent, allowed));
	let unlimited = $derived(isUnlimited(allowed));
	let remaining = $derived(Math.max(0, allowed - spent));
	let color = $derived(
		spentPercent >= 100 ? 'var(--danger)' : spentPercent >= 80 ? 'var(--warning)' : 'var(--primary)'
	);
</script>

<div class="min-w-[140px]">
	<div class="flex items-baseline justify-between gap-2 text-xs tabular-nums">
		<span class="font-mono">{bytes(used)}</span>
		<span class="font-mono" style="color: var(--text-faint)">
			{unlimited ? '∞' : bytes(allowed)}
		</span>
	</div>

	<div
		class="relative mt-1 h-1 w-full overflow-hidden rounded-full"
		style="background: var(--surface-3)"
	>
		{#if !unlimited}
			<!-- The whole group's spending, dimmed, with this peer's share on top. -->
			<div
				class="absolute inset-y-0 left-0 rounded-full transition-all"
				style="width: {spentPercent}%; background: {color}; opacity: 0.3"
			></div>
			<div
				class="absolute inset-y-0 left-0 rounded-full transition-all"
				style="width: {percent}%; background: {color}"
			></div>
		{/if}
	</div>

	{#if !compact}
		<div
			class="mt-0.5 flex items-baseline justify-between gap-2 text-[10px] tabular-nums"
			style="color: var(--text-faint)"
		>
			{#if unlimited}
				<span>no limit</span>
			{:else}
				<span>{percent}% used</span>
				<span class="font-mono">{bytes(remaining)} left</span>
			{/if}
		</div>
	{/if}

	{#if source}
		<div class="mt-0.5 text-[10px] tabular-nums" style="color: var(--text-faint)">
			via {source}{unlimited ? '' : `, group at ${spentPercent}%`}
		</div>
	{/if}
</div>
