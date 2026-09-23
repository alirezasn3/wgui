<script lang="ts">
	import { parseMarkdown, type Inline } from '$lib/markdown';

	let { source }: { source: string } = $props();
	let blocks = $derived(parseMarkdown(source));
</script>

{#snippet inline(parts: Inline[])}
	{#each parts as part, i (i)}
		{#if part.kind === 'code'}
			<code class="rounded px-1 font-mono text-[0.9em]" style="background: var(--surface-3)"
				>{part.text}</code
			>
		{:else if part.kind === 'strong'}
			<strong class="font-semibold" style="color: var(--text)">{part.text}</strong>
		{:else if part.kind === 'em'}
			<em>{part.text}</em>
		{:else if part.kind === 'link'}
			<a
				href={part.href}
				target="_blank"
				rel="noopener noreferrer"
				class="hover:underline"
				style="color: var(--primary)">{part.text}</a
			>
		{:else}
			{part.text}
		{/if}
	{/each}
{/snippet}

<div class="space-y-2 text-sm leading-relaxed" style="color: var(--text-muted)">
	{#each blocks as block, i (i)}
		{#if block.kind === 'heading'}
			<div
				class="pt-1 font-semibold {block.level <= 2
					? 'text-sm'
					: 'text-xs uppercase tracking-wide'}"
				style="color: var(--text)"
			>
				{@render inline(block.inline)}
			</div>
		{:else if block.kind === 'list'}
			{#if block.ordered}
				<ol class="list-decimal space-y-1 pl-5">
					{#each block.items as item, j (j)}<li>{@render inline(item)}</li>{/each}
				</ol>
			{:else}
				<ul class="list-disc space-y-1 pl-5">
					{#each block.items as item, j (j)}<li>{@render inline(item)}</li>{/each}
				</ul>
			{/if}
		{:else if block.kind === 'code'}
			<pre
				class="overflow-x-auto rounded-md px-3 py-2 font-mono text-xs"
				style="background: var(--surface-2)">{block.text}</pre>
		{:else}
			<p>{@render inline(block.inline)}</p>
		{/if}
	{/each}
</div>
