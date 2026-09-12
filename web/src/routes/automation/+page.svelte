<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { app, poll } from '$lib/app.svelte';
	import Confirm from '$lib/components/Confirm.svelte';
	import Empty from '$lib/components/Empty.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import MonitorEditor from '$lib/components/MonitorEditor.svelte';
	import ScriptEditor from '$lib/components/ScriptEditor.svelte';
	import { relative } from '$lib/format';
	import type { Monitor, Script, ScriptRun } from '$lib/types';
	import { onMount } from 'svelte';

	let scripts = $state<Script[]>([]);
	let monitors = $state<Monitor[]>([]);
	let loadError = $state('');
	let firstLoad = $state(true);

	let editingScript = $state<Script | null>(null);
	let scriptEditorOpen = $state(false);
	let editingMonitor = $state<Monitor | null>(null);
	let monitorEditorOpen = $state(false);

	let runningNow = $state<Set<number>>(new Set());
	let output = $state<{ script: Script; run: ScriptRun } | null>(null);
	let checking = $state<number | null>(null);
	let confirming = $state<{ title: string; message: string; run: () => Promise<void> } | null>(
		null
	);
	let confirmBusy = $state(false);

	onMount(() => {
		if (!app.session?.isAdmin) {
			void goto('/');
			return;
		}
		return poll(load, 3000);
	});

	async function load() {
		try {
			const [s, m] = await Promise.all([api.scripts(), api.monitors()]);
			scripts = s.scripts;
			monitors = m.monitors;
			loadError = '';
		} catch (e) {
			loadError = e instanceof Error ? e.message : String(e);
		} finally {
			firstLoad = false;
		}
	}

	async function run(script: Script) {
		runningNow = new Set([...runningNow, script.id]);
		try {
			const result = await api.runScript(script.id);
			output = { script, run: result };
			if (result.error) {
				app.notify('error', `${script.name}: ${result.error}`);
			} else if (result.exitCode !== 0) {
				app.notify('error', `${script.name} exited ${result.exitCode}`);
			} else {
				app.success(`${script.name} finished`);
			}
			await load();
		} catch (e) {
			app.fail(e);
		} finally {
			const next = new Set(runningNow);
			next.delete(script.id);
			runningNow = next;
		}
	}

	async function stop(script: Script) {
		try {
			await api.stopScript(script.id);
			app.notify('info', `Stopping ${script.name}`);
		} catch (e) {
			app.fail(e);
		}
	}

	async function checkNow(monitor: Monitor) {
		checking = monitor.id;
		try {
			const res = await api.checkMonitor(monitor.id);
			if (res.ok) {
				app.success(`${monitor.target} answered in ${res.rttMs} ms`);
			} else {
				app.notify('error', res.error ?? `${monitor.target} did not answer`);
			}
		} catch (e) {
			app.fail(e);
		} finally {
			checking = null;
		}
	}

	async function toggleMonitor(monitor: Monitor) {
		try {
			await api.updateMonitor(monitor.id, { enabled: !monitor.enabled });
			await load();
		} catch (e) {
			app.fail(e);
		}
	}

	async function resetMonitor(monitor: Monitor) {
		try {
			await api.updateMonitor(monitor.id, { resetState: true });
			app.success(`${monitor.name} re-armed`);
			await load();
		} catch (e) {
			app.fail(e);
		}
	}

	function askDeleteScript(script: Script) {
		const used = monitors.filter((m) => m.scriptId === script.id);
		confirming = {
			title: `Delete ${script.name}?`,
			message: used.length
				? `${used.map((m) => m.name).join(', ')} ${used.length === 1 ? 'runs' : 'run'} this script and will be left with nothing to run.`
				: 'The script and its history are removed. This cannot be undone.',
			run: async () => {
				await api.deleteScript(script.id);
				app.success(`Deleted ${script.name}`);
				await load();
			}
		};
	}

	function askDeleteMonitor(monitor: Monitor) {
		confirming = {
			title: `Delete ${monitor.name}?`,
			message: 'The destination stops being watched. The script it ran is left alone.',
			run: async () => {
				await api.deleteMonitor(monitor.id);
				app.success(`Deleted ${monitor.name}`);
				await load();
			}
		};
	}

	async function confirmRun() {
		if (!confirming) return;
		confirmBusy = true;
		try {
			await confirming.run();
			confirming = null;
		} catch (e) {
			app.fail(e);
		} finally {
			confirmBusy = false;
		}
	}

	function monitorState(m: Monitor): { label: string; bg: string; fg: string } {
		if (!m.enabled) return { label: 'Paused', bg: 'var(--surface-3)', fg: 'var(--text-muted)' };
		if (m.firedAt) return { label: 'Fired', bg: 'var(--danger-soft)', fg: 'var(--danger)' };
		if (m.consecutiveFailures > 0)
			return {
				label: `Failing ${m.consecutiveFailures}/${m.failuresBefore}`,
				bg: 'var(--warning-soft)',
				fg: 'var(--warning)'
			};
		if (!m.lastCheckedAt)
			return { label: 'Pending', bg: 'var(--surface-3)', fg: 'var(--text-muted)' };
		return { label: 'Reachable', bg: 'var(--success-soft)', fg: 'var(--success)' };
	}
</script>

<div class="mb-4">
	<h1 class="text-xl font-semibold">Automation</h1>
	<p class="text-xs" style="color: var(--text-muted)">
		Scripts you can run from here, and destinations that run one when they stop answering
	</p>
</div>

{#if loadError}
	<div class="card mb-3 p-3 text-sm" style="border-color: var(--danger); color: var(--danger)">
		{loadError}
	</div>
{/if}

<!-- Scripts -->
<section class="card mb-5 overflow-hidden">
	<header
		class="flex items-center justify-between gap-3 border-b px-4 py-2.5"
		style="border-color: var(--border)"
	>
		<div>
			<h2 class="text-sm font-semibold">Scripts</h2>
			<p class="text-xs" style="color: var(--text-muted)">Run as root on this server</p>
		</div>
		<button
			class="btn btn-primary btn-sm"
			onclick={() => {
				editingScript = null;
				scriptEditorOpen = true;
			}}
		>
			<Icon name="plus" size={14} /> New script
		</button>
	</header>

	{#if scripts.length === 0 && !firstLoad}
		<Empty
			title="No scripts yet"
			hint="Save the commands you would otherwise SSH in to run — restarting WireGuard, reloading firewall rules, rotating a route."
		/>
	{:else}
		<ul>
			{#each scripts as script (script.id)}
				{@const busy = script.running || runningNow.has(script.id)}
				<li class="border-b px-4 py-3 last:border-0" style="border-color: var(--border)">
					<div class="flex flex-wrap items-start justify-between gap-3">
						<div class="min-w-0 flex-1">
							<div class="flex items-center gap-2">
								<span class="font-medium">{script.name}</span>
								{#if busy}
									<span class="tag" style="background: var(--info-soft); color: var(--info)">
										Running
									</span>
								{/if}
							</div>
							{#if script.description}
								<p class="mt-0.5 text-xs" style="color: var(--text-muted)">{script.description}</p>
							{/if}
							{#if script.lastRun}
								<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
									Last run {relative(script.lastRun.startedAt)}
									{#if script.lastRun.source === 'monitor'}by {script.lastRun
											.actor}{:else if script.lastRun.actor}by {script.lastRun.actor}{/if}
									·
									{#if script.lastRun.error}
										<span style="color: var(--danger)">{script.lastRun.error}</span>
									{:else if script.lastRun.exitCode !== 0}
										<span style="color: var(--danger)">exited {script.lastRun.exitCode}</span>
									{:else}
										<span style="color: var(--success)">succeeded</span>
									{/if}
								</p>
							{/if}
						</div>

						<div class="flex shrink-0 items-center gap-1">
							{#if script.lastRun}
								<button
									class="btn btn-ghost btn-sm"
									onclick={() => (output = { script, run: script.lastRun! })}
								>
									Output
								</button>
							{/if}
							{#if busy}
								<button class="btn btn-danger btn-sm" onclick={() => stop(script)}>Stop</button>
							{:else}
								<button class="btn btn-primary btn-sm" onclick={() => run(script)}>
									<Icon name="power" size={13} /> Run
								</button>
							{/if}
							<button
								class="btn btn-ghost btn-icon"
								onclick={() => {
									editingScript = script;
									scriptEditorOpen = true;
								}}
								aria-label="Edit {script.name}"
							>
								<Icon name="edit" size={15} />
							</button>
							<button
								class="btn btn-ghost btn-icon"
								style="color: var(--danger)"
								onclick={() => askDeleteScript(script)}
								aria-label="Delete {script.name}"
							>
								<Icon name="trash" size={15} />
							</button>
						</div>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</section>

<!-- Monitors -->
<section class="card overflow-hidden">
	<header
		class="flex items-center justify-between gap-3 border-b px-4 py-2.5"
		style="border-color: var(--border)"
	>
		<div>
			<h2 class="text-sm font-semibold">Monitors</h2>
			<p class="text-xs" style="color: var(--text-muted)">
				Ping or connect to a destination, and run a script when it stops answering
			</p>
		</div>
		<button
			class="btn btn-primary btn-sm"
			onclick={() => {
				editingMonitor = null;
				monitorEditorOpen = true;
			}}
		>
			<Icon name="plus" size={14} /> New monitor
		</button>
	</header>

	{#if monitors.length === 0 && !firstLoad}
		<Empty
			title="No monitors yet"
			hint="Watch an upstream gateway or a service, and have wgui react when it goes away."
		/>
	{:else}
		<ul>
			{#each monitors as monitor (monitor.id)}
				{@const state = monitorState(monitor)}
				<li class="border-b px-4 py-3 last:border-0" style="border-color: var(--border)">
					<div class="flex flex-wrap items-start justify-between gap-3">
						<div class="min-w-0 flex-1">
							<div class="flex flex-wrap items-center gap-2">
								<span class="font-medium">{monitor.name}</span>
								<span class="tag" style="background: {state.bg}; color: {state.fg}">
									{state.label}
								</span>
								<span class="font-mono text-xs" style="color: var(--text-muted)">
									{monitor.target}
								</span>
							</div>
							<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
								Every {monitor.intervalSec}s · fires after {monitor.failuresBefore}
								{monitor.failuresBefore === 1 ? 'failure' : 'failures'} ·
								{#if monitor.scriptName}
									runs <span style="color: var(--text-muted)">{monitor.scriptName}</span>
								{:else}
									watch only
								{/if}
								{#if monitor.lastCheckedAt}
									· checked {relative(monitor.lastCheckedAt)}
									{#if monitor.consecutiveFailures === 0 && monitor.lastRttMs > 0}
										in {monitor.lastRttMs} ms
									{/if}
								{/if}
							</p>
							{#if monitor.lastError && monitor.consecutiveFailures > 0}
								<p class="mt-0.5 text-[11px]" style="color: var(--danger)">{monitor.lastError}</p>
							{/if}
						</div>

						<div class="flex shrink-0 items-center gap-1">
							{#if monitor.firedAt}
								<button class="btn btn-default btn-sm" onclick={() => resetMonitor(monitor)}>
									Re-arm
								</button>
							{/if}
							<button
								class="btn btn-default btn-sm"
								onclick={() => checkNow(monitor)}
								disabled={checking === monitor.id}
							>
								{checking === monitor.id ? 'Checking…' : 'Check now'}
							</button>
							<button
								class="btn btn-ghost btn-icon"
								onclick={() => toggleMonitor(monitor)}
								aria-label={monitor.enabled ? 'Pause' : 'Resume'}
								title={monitor.enabled ? 'Pause' : 'Resume'}
							>
								<Icon name="power" size={15} />
							</button>
							<button
								class="btn btn-ghost btn-icon"
								onclick={() => {
									editingMonitor = monitor;
									monitorEditorOpen = true;
								}}
								aria-label="Edit {monitor.name}"
							>
								<Icon name="edit" size={15} />
							</button>
							<button
								class="btn btn-ghost btn-icon"
								style="color: var(--danger)"
								onclick={() => askDeleteMonitor(monitor)}
								aria-label="Delete {monitor.name}"
							>
								<Icon name="trash" size={15} />
							</button>
						</div>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</section>

<ScriptEditor
	open={scriptEditorOpen}
	script={editingScript}
	onclose={() => (scriptEditorOpen = false)}
	onsaved={load}
/>

<MonitorEditor
	open={monitorEditorOpen}
	monitor={editingMonitor}
	{scripts}
	onclose={() => (monitorEditorOpen = false)}
	onsaved={load}
/>

<Confirm
	open={confirming !== null}
	title={confirming?.title ?? ''}
	message={confirming?.message ?? ''}
	confirmLabel="Delete"
	danger
	busy={confirmBusy}
	onconfirm={confirmRun}
	oncancel={() => (confirming = null)}
/>

{#if output}
	{@const run = output.run}
	<div class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 sm:p-8">
		<div
			class="fixed inset-0 bg-black/60"
			role="button"
			tabindex="-1"
			aria-label="Close"
			onclick={() => (output = null)}
			onkeydown={(e) => e.key === 'Enter' && (output = null)}
		></div>
		<div class="card relative z-10 my-auto w-full max-w-3xl shadow-2xl">
			<header
				class="flex items-start justify-between gap-4 border-b px-5 py-3.5"
				style="border-color: var(--border)"
			>
				<div class="min-w-0">
					<h2 class="truncate text-base font-semibold">{output.script.name}</h2>
					<p class="mt-0.5 text-xs" style="color: var(--text-muted)">
						{relative(run.startedAt)} · {run.endedAt - run.startedAt} ms ·
						{#if run.error}
							<span style="color: var(--danger)">{run.error}</span>
						{:else}
							exit {run.exitCode}
						{/if}
						{#if run.source === 'monitor'}· started by {run.actor}{/if}
					</p>
				</div>
				<button
					class="btn btn-ghost btn-icon shrink-0"
					onclick={() => (output = null)}
					aria-label="Close"
				>
					<Icon name="close" />
				</button>
			</header>
			<div class="px-5 py-4">
				{#if run.output}
					<pre
						class="max-h-[26rem] overflow-auto rounded-md p-3 font-mono text-[11px] leading-relaxed"
						style="background: var(--surface-2)">{run.output}</pre>
				{:else}
					<p class="py-6 text-center text-sm" style="color: var(--text-faint)">No output.</p>
				{/if}
			</div>
		</div>
	</div>
{/if}
