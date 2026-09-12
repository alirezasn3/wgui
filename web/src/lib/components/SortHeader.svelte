<script lang="ts">
	import Icon from './Icon.svelte';

	interface Props {
		label: string;
		field: string;
		sort: string;
		order: 'asc' | 'desc';
		onsort: (field: string) => void;
		align?: 'left' | 'right';
		class?: string;
	}
	let {
		label,
		field,
		sort,
		order,
		onsort,
		align = 'left',
		class: className = ''
	}: Props = $props();

	let active = $derived(sort === field);
</script>

<th class="px-3 py-2 font-medium {className}" style="text-align: {align}">
	<button
		class="inline-flex cursor-pointer items-center gap-1 transition-colors hover:text-[var(--text)]"
		style="color: {active ? 'var(--text)' : 'var(--text-muted)'}"
		onclick={() => onsort(field)}
		aria-label="Sort by {label}"
	>
		{label}
		<span style="opacity: {active ? 1 : 0.35}">
			<Icon name={active ? (order === 'asc' ? 'up' : 'down') : 'sort'} size={12} />
		</span>
	</button>
</th>
