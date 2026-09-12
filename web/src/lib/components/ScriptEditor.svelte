<script lang="ts">
	import { api } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import type { Script } from '$lib/types';
	import Modal from './Modal.svelte';

	interface Props {
		open: boolean;
		script: Script | null;
		onclose: () => void;
		onsaved: () => void;
	}
	let { open, script, onclose, onsaved }: Props = $props();

	let name = $state('');
	let description = $state('');
	let body = $state('');
	let timeoutSec = $state(60);
	let saving = $state(false);
	let error = $state('');

	$effect(() => {
		if (!open) return;
		name = script?.name ?? '';
		description = script?.description ?? '';
		body = script?.body ?? '#!/bin/bash\nset -euo pipefail\n\n';
		timeoutSec = script?.timeoutSec ?? 60;
		error = '';
	});

	async function save() {
		error = '';
		if (!name.trim()) {
			error = 'A name is required';
			return;
		}

		saving = true;
		try {
			const input = { name: name.trim(), description, body, timeoutSec };
			const saved = script
				? await api.updateScript(script.id, input)
				: await api.createScript(input);
			app.success(script ? `Updated ${saved.name}` : `Created ${saved.name}`);
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
	title={script ? `Edit ${script.name}` : 'New script'}
	subtitle="Runs on this server as root, the same as everything else wgui does"
	width="44rem"
	{onclose}
>
	<div class="space-y-4">
		<div class="grid gap-4 sm:grid-cols-[2fr_1fr]">
			<div>
				<label class="label" for="script-name">Name</label>
				<input
					id="script-name"
					class="field"
					bind:value={name}
					placeholder="e.g. restart-wireguard"
					autocomplete="off"
				/>
			</div>
			<div>
				<label class="label" for="script-timeout">Timeout</label>
				<div class="relative">
					<input
						id="script-timeout"
						class="field pr-16"
						type="number"
						min="1"
						max="1800"
						bind:value={timeoutSec}
					/>
					<span
						class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
						style="color: var(--text-faint)">seconds</span
					>
				</div>
			</div>
		</div>

		<div>
			<label class="label" for="script-description">Description</label>
			<input
				id="script-description"
				class="field"
				bind:value={description}
				placeholder="What it does, and when you would run it"
				autocomplete="off"
			/>
		</div>

		<div>
			<label class="label" for="script-body">Script</label>
			<textarea
				id="script-body"
				class="field font-mono text-xs"
				rows="16"
				bind:value={body}
				spellcheck="false"
			></textarea>
			<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
				Run with bash on a fixed <span class="font-mono">PATH</span>, nothing written to disk. The
				environment is clean apart from <span class="font-mono">WGUI=1</span>, so a script can tell
				it was started from here.
			</p>
		</div>

		{#if error}
			<p class="text-sm" style="color: var(--danger)">{error}</p>
		{/if}
	</div>

	{#snippet footer()}
		<button class="btn btn-default" onclick={onclose} disabled={saving}>Cancel</button>
		<button class="btn btn-primary" onclick={save} disabled={saving}>
			{saving ? 'Saving…' : script ? 'Save changes' : 'Create script'}
		</button>
	{/snippet}
</Modal>
