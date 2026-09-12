<script lang="ts">
	import { api } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import type { Monitor, Script } from '$lib/types';
	import Modal from './Modal.svelte';

	interface Props {
		open: boolean;
		monitor: Monitor | null;
		scripts: Script[];
		onclose: () => void;
		onsaved: () => void;
	}
	let { open, monitor, scripts, onclose, onsaved }: Props = $props();

	let name = $state('');
	let target = $state('');
	let intervalSec = $state(30);
	let timeoutSec = $state(5);
	let failuresBefore = $state(3);
	let scriptId = $state(0);
	let rearmOnRecovery = $state(true);
	let enabled = $state(true);
	let saving = $state(false);
	let error = $state('');

	$effect(() => {
		if (!open) return;
		name = monitor?.name ?? '';
		target = monitor?.target ?? '';
		intervalSec = monitor?.intervalSec ?? 30;
		timeoutSec = monitor?.timeoutSec ?? 5;
		failuresBefore = monitor?.failuresBefore ?? 3;
		scriptId = monitor?.scriptId ?? 0;
		rearmOnRecovery = monitor?.rearmOnRecovery ?? true;
		enabled = monitor?.enabled ?? true;
		error = '';
	});

	// A port turns the check into a TCP connection, which says whether the
	// service is up rather than whether the host answers ping.
	let usesTCP = $derived(/:\d+$/.test(target.trim()));
	let blindFor = $derived(Math.round(intervalSec * failuresBefore));

	async function save() {
		error = '';
		if (!name.trim()) {
			error = 'A name is required';
			return;
		}
		if (!target.trim()) {
			error = 'A target is required';
			return;
		}

		saving = true;
		try {
			const input = {
				name: name.trim(),
				target: target.trim(),
				intervalSec,
				timeoutSec,
				failuresBefore,
				scriptId,
				rearmOnRecovery,
				enabled
			};
			const saved = monitor
				? await api.updateMonitor(monitor.id, input)
				: await api.createMonitor(input);
			app.success(monitor ? `Updated ${saved.name}` : `Created ${saved.name}`);
			onsaved();
			onclose();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			saving = false;
		}
	}
</script>

<Modal
	{open}
	title={monitor ? `Edit ${monitor.name}` : 'New monitor'}
	subtitle="Watches a destination and runs a script when it stops answering"
	width="32rem"
	{onclose}
>
	<div class="space-y-4">
		<div>
			<label class="label" for="monitor-name">Name</label>
			<input
				id="monitor-name"
				class="field"
				bind:value={name}
				placeholder="e.g. upstream gateway"
				autocomplete="off"
			/>
		</div>

		<div>
			<label class="label" for="monitor-target">Target</label>
			<input
				id="monitor-target"
				class="field font-mono text-sm"
				bind:value={target}
				placeholder="1.1.1.1, example.com, or example.com:443"
				autocomplete="off"
			/>
			<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
				{#if usesTCP}
					Checked by opening a TCP connection, which tells you the service is actually up.
				{:else}
					Checked with an ICMP ping. Add a port to check a service instead — plenty of hosts drop
					ping, and a monitor cannot tell that apart from being down.
				{/if}
			</p>
		</div>

		<div class="grid gap-4 sm:grid-cols-3">
			<div>
				<label class="label" for="monitor-interval">Check every</label>
				<div class="relative">
					<input
						id="monitor-interval"
						class="field pr-8"
						type="number"
						min="5"
						bind:value={intervalSec}
					/>
					<span
						class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
						style="color: var(--text-faint)">s</span
					>
				</div>
			</div>
			<div>
				<label class="label" for="monitor-timeout">Wait up to</label>
				<div class="relative">
					<input
						id="monitor-timeout"
						class="field pr-8"
						type="number"
						min="1"
						max="60"
						bind:value={timeoutSec}
					/>
					<span
						class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
						style="color: var(--text-faint)">s</span
					>
				</div>
			</div>
			<div>
				<label class="label" for="monitor-failures">Failures first</label>
				<input
					id="monitor-failures"
					class="field"
					type="number"
					min="1"
					bind:value={failuresBefore}
				/>
			</div>
		</div>
		<p class="-mt-2 text-[11px]" style="color: var(--text-faint)">
			The script runs after {failuresBefore} failed {failuresBefore === 1 ? 'check' : 'checks'} in a row
			— roughly {blindFor} seconds of the target being unreachable.
		</p>

		<div>
			<label class="label" for="monitor-script">Run this script</label>
			<select id="monitor-script" class="field" bind:value={scriptId}>
				<option value={0}>Nothing — just watch</option>
				{#each scripts as script (script.id)}
					<option value={script.id}>{script.name}</option>
				{/each}
			</select>
		</div>

		<label class="flex cursor-pointer items-start gap-2 text-sm">
			<input
				type="checkbox"
				class="mt-0.5 accent-[var(--primary)]"
				bind:checked={rearmOnRecovery}
			/>
			<span>
				Run the script once per outage
				<span class="block text-[11px]" style="color: var(--text-faint)">
					Stays quiet until the target answers again. Unticked, it runs on every failed check for as
					long as the outage lasts.
				</span>
			</span>
		</label>

		<label class="flex cursor-pointer items-center gap-2 text-sm">
			<input type="checkbox" class="accent-[var(--primary)]" bind:checked={enabled} />
			Enabled
		</label>

		{#if error}
			<p class="text-sm" style="color: var(--danger)">{error}</p>
		{/if}
	</div>

	{#snippet footer()}
		<button class="btn btn-default" onclick={onclose} disabled={saving}>Cancel</button>
		<button class="btn btn-primary" onclick={save} disabled={saving}>
			{saving ? 'Saving…' : monitor ? 'Save changes' : 'Create monitor'}
		</button>
	{/snippet}
</Modal>
