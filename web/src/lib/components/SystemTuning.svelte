<script lang="ts">
	import { api } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import type { SystemStatus } from '$lib/types';
	import { onMount } from 'svelte';
	import Icon from './Icon.svelte';

	let status = $state<SystemStatus | null>(null);
	let busy = $state('');
	let loadError = $state('');

	onMount(load);

	async function load() {
		try {
			status = await api.system();
			loadError = '';
		} catch (e) {
			loadError = e instanceof Error ? e.message : String(e);
		}
	}

	async function run(what: string, action: () => Promise<SystemStatus>, done: string) {
		busy = what;
		try {
			status = await action();
			app.success(done);
		} catch (e) {
			app.fail(e);
		} finally {
			busy = '';
		}
	}

	// Both changes need root, because they write to /proc/sys and /etc/sysctl.d.
	let blocked = $derived(!status?.supported || !status?.writable);
	let blockedReason = $derived(
		!status?.supported
			? 'These are Linux kernel settings, and this server is not running Linux.'
			: !status?.writable
				? 'wgui is not running as root, so it cannot change kernel settings. Run it as root, or apply these with sysctl yourself.'
				: ''
	);
</script>

<section class="card p-4">
	<h2 class="mb-1 text-sm font-semibold">Server tuning</h2>
	<p class="mb-3 text-xs" style="color: var(--text-muted)">
		Kernel settings that affect how well the tunnel works. Each is written to the running kernel and
		to <span class="font-mono">/etc/sysctl.d</span>, so it survives a reboot.
	</p>

	{#if loadError}
		<p class="text-sm" style="color: var(--danger)">{loadError}</p>
	{:else if status}
		{#if blocked}
			<div
				class="mb-3 rounded-md px-3 py-2 text-xs"
				style="background: var(--warning-soft); color: var(--warning)"
			>
				{blockedReason}
			</div>
		{/if}

		<div class="space-y-3">
			<div
				class="flex flex-wrap items-center justify-between gap-3 rounded-md px-3 py-2.5"
				style="background: var(--surface-2)"
			>
				<div class="min-w-0">
					<div class="flex items-center gap-2 text-sm font-medium">
						IP forwarding
						{#if status.ipForwardingV4}
							<span class="tag" style="background: var(--success-soft); color: var(--success)"
								>On</span
							>
						{:else}
							<span class="tag" style="background: var(--danger-soft); color: var(--danger)"
								>Off</span
							>
						{/if}
					</div>
					<p class="mt-0.5 text-xs" style="color: var(--text-muted)">
						Without it the tunnel connects but no traffic reaches the internet.
						{#if status.supported}
							IPv4 {status.ipForwardingV4 ? 'on' : 'off'}, IPv6
							{status.ipForwardingV6 ? 'on' : 'off'}.
						{/if}
					</p>
				</div>
				<button
					class="btn btn-primary btn-sm shrink-0"
					disabled={blocked || busy !== '' || status.ipForwardingV4}
					onclick={() => run('forwarding', api.enableIPForwarding, 'IP forwarding enabled')}
				>
					{#if busy === 'forwarding'}
						Enabling…
					{:else if status.ipForwardingV4}
						<Icon name="check" size={13} /> Enabled
					{:else}
						Enable
					{/if}
				</button>
			</div>

			<div
				class="flex flex-wrap items-center justify-between gap-3 rounded-md px-3 py-2.5"
				style="background: var(--surface-2)"
			>
				<div class="min-w-0">
					<div class="flex items-center gap-2 text-sm font-medium">
						BBR congestion control
						{#if status.bbrEnabled}
							<span class="tag" style="background: var(--success-soft); color: var(--success)"
								>On</span
							>
						{:else}
							<span class="tag" style="background: var(--surface-3); color: var(--text-muted)">
								{status.congestionControl || 'unknown'}
							</span>
						{/if}
					</div>
					<p class="mt-0.5 text-xs" style="color: var(--text-muted)">
						Usually improves throughput over long-distance links. Also selects the
						<span class="font-mono">fq</span> queueing discipline, which BBR expects.
						{#if status.supported && !status.bbrAvailable}
							<span style="color: var(--warning)">
								This kernel does not offer BBR — load the tcp_bbr module first.
							</span>
						{/if}
					</p>
				</div>
				<button
					class="btn btn-primary btn-sm shrink-0"
					disabled={blocked || busy !== '' || status.bbrEnabled || !status.bbrAvailable}
					onclick={() => run('bbr', () => api.setCongestion('bbr'), 'BBR enabled')}
				>
					{#if busy === 'bbr'}
						Enabling…
					{:else if status.bbrEnabled}
						<Icon name="check" size={13} /> Enabled
					{:else}
						Enable
					{/if}
				</button>
			</div>
		</div>

		{#if status.supported && status.defaultQdisc}
			<p class="mt-3 text-[11px]" style="color: var(--text-faint)">
				Queueing discipline: <span class="font-mono">{status.defaultQdisc}</span>. Available
				algorithms: <span class="font-mono">{status.availableAlgos.join(', ')}</span>.
			</p>
		{/if}
	{/if}
</section>
