<script lang="ts">
	import { api } from '$lib/api';
	import type { RuleSet } from '$lib/types';
	import CopyButton from './CopyButton.svelte';
	import Icon from './Icon.svelte';

	let open = $state(false);
	let rules = $state<RuleSet[]>([]);
	let active = $state('');
	let loading = $state(false);
	let loadError = $state('');
	let loadedAt = $state(0);

	// Firewall dumps are expensive to produce and rarely change, so they are
	// fetched when the section is opened rather than on the dashboard's poll.
	async function load() {
		loading = true;
		loadError = '';
		try {
			const res = await api.networkRules();
			rules = res.rules;
			loadedAt = Date.now();
			if (!rules.some((r) => r.name === active)) {
				// Open on the first section that actually has something to show.
				active = (rules.find((r) => r.available && !r.error) ?? rules[0])?.name ?? '';
			}
		} catch (e) {
			loadError = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	function toggle() {
		open = !open;
		if (open && loadedAt === 0) void load();
	}

	let current = $derived(rules.find((r) => r.name === active));
</script>

<section class="card overflow-hidden">
	<button
		class="flex w-full cursor-pointer items-center justify-between gap-3 px-4 py-2.5 text-left transition-colors hover:bg-[var(--surface-2)]"
		onclick={toggle}
		aria-expanded={open}
	>
		<div class="min-w-0">
			<h2 class="text-sm font-semibold">Network rules</h2>
			<p class="text-xs" style="color: var(--text-muted)">
				The firewall and routing configuration of this server, read-only
			</p>
		</div>
		<span class="shrink-0" style="color: var(--text-muted)">
			<Icon name={open ? 'up' : 'down'} size={16} />
		</span>
	</button>

	{#if open}
		<div class="border-t" style="border-color: var(--border)">
			{#if loadError}
				<p class="px-4 py-6 text-sm" style="color: var(--danger)">{loadError}</p>
			{:else if loading && rules.length === 0}
				<p class="px-4 py-6 text-center text-xs" style="color: var(--text-faint)">Reading…</p>
			{:else if rules.length > 0}
				<div
					class="flex flex-wrap items-center gap-1 border-b px-3 py-2"
					style="border-color: var(--border); background: var(--surface-2)"
				>
					{#each rules as rule (rule.name)}
						<button
							class="btn btn-sm {active === rule.name ? 'btn-primary' : 'btn-ghost'}"
							onclick={() => (active = rule.name)}
						>
							{rule.name}
							{#if !rule.available}
								<span class="opacity-60">·</span>
							{/if}
						</button>
					{/each}

					<div class="ml-auto flex items-center gap-2">
						{#if loadedAt}
							<span class="text-[11px]" style="color: var(--text-faint)">
								read {new Date(loadedAt).toLocaleTimeString()}
							</span>
						{/if}
						{#if current?.output}
							<CopyButton
								value={current.output}
								class="btn btn-default btn-sm"
								title="Copy output"
							/>
						{/if}
						<button class="btn btn-default btn-sm" onclick={load} disabled={loading}>
							<Icon name="refresh" size={13} />
							{loading ? 'Reading…' : 'Refresh'}
						</button>
					</div>
				</div>

				{#if current}
					<div class="px-4 py-3">
						{#if current.command}
							<div class="mb-2 font-mono text-[11px]" style="color: var(--text-faint)">
								$ {current.command}
							</div>
						{/if}

						{#if !current.available}
							<p class="py-4 text-sm" style="color: var(--text-muted)">
								{current.error === 'not installed'
									? `${current.name} is not installed on this server.`
									: current.error}
							</p>
						{:else}
							{#if current.error}
								<p
									class="mb-2 rounded-md px-3 py-2 text-xs"
									style="background: var(--warning-soft); color: var(--warning)"
								>
									{current.error}
								</p>
							{/if}
							{#if current.output}
								<pre
									class="max-h-[28rem] overflow-auto rounded-md p-3 font-mono text-[11px] leading-relaxed"
									style="background: var(--surface-2)">{current.output}</pre>
								{#if current.truncated}
									<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
										Output was long and has been cut short.
									</p>
								{/if}
							{:else}
								<p class="py-4 text-sm" style="color: var(--text-faint)">No output.</p>
							{/if}
						{/if}
					</div>
				{/if}
			{/if}
		</div>
	{/if}
</section>
