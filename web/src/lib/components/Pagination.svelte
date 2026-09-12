<script lang="ts">
	import Icon from './Icon.svelte';

	interface Props {
		page: number;
		pageSize: number;
		total: number;
		onpage: (page: number) => void;
		onpageSize: (size: number) => void;
	}
	let { page, pageSize, total, onpage, onpageSize }: Props = $props();

	let pages = $derived(Math.max(1, Math.ceil(total / pageSize)));
	let from = $derived(total === 0 ? 0 : (page - 1) * pageSize + 1);
	let to = $derived(Math.min(page * pageSize, total));
</script>

<div class="flex flex-wrap items-center justify-between gap-3 px-3 py-2.5 text-xs">
	<div style="color: var(--text-muted)">
		{#if total === 0}
			No results
		{:else}
			Showing <span class="font-medium" style="color: var(--text)">{from}–{to}</span>
			of <span class="font-medium" style="color: var(--text)">{total}</span>
		{/if}
	</div>

	<div class="flex items-center gap-2">
		<select
			class="field w-auto py-1 text-xs"
			value={pageSize}
			onchange={(e) => onpageSize(Number(e.currentTarget.value))}
			aria-label="Rows per page"
		>
			{#each [25, 50, 100, 200] as size (size)}
				<option value={size}>{size} / page</option>
			{/each}
		</select>

		<div class="flex items-center gap-1">
			<button
				class="btn btn-default btn-icon"
				disabled={page <= 1}
				onclick={() => onpage(page - 1)}
				aria-label="Previous page"
			>
				<Icon name="chevronLeft" size={14} />
			</button>
			<span class="px-1 tabular-nums" style="color: var(--text-muted)">{page} / {pages}</span>
			<button
				class="btn btn-default btn-icon"
				disabled={page >= pages}
				onclick={() => onpage(page + 1)}
				aria-label="Next page"
			>
				<Icon name="chevronRight" size={14} />
			</button>
		</div>
	</div>
</div>
