<script lang="ts">
	import { app } from '$lib/app.svelte';
	import { bytes, relative } from '$lib/format';
	import { updates } from '$lib/updates.svelte';
	import { onMount } from 'svelte';
	import Confirm from './Confirm.svelte';
	import Icon from './Icon.svelte';
	import Markdown from './Markdown.svelte';

	let checking = $state(false);
	let confirming = $state(false);
	let starting = $state(false);

	onMount(() => {
		void updates.refresh();
	});

	let status = $derived(updates.status);
	let latest = $derived(status?.releases[0]);
	let job = $derived(status?.job);
	let busy = $derived(
		!!updates.installing ||
			(job && ['downloading', 'verifying', 'installing', 'restarting'].includes(job.state))
	);

	async function check() {
		checking = true;
		try {
			await updates.check();
			const s = updates.status;
			if (s?.checkError) app.fail(new Error(s.checkError), 'Could not check for updates');
			else if (s && !s.available) app.success('You are on the latest release');
		} catch (e) {
			app.fail(e);
		} finally {
			checking = false;
		}
	}

	async function install() {
		if (!latest) return;
		starting = true;
		try {
			await updates.install(latest.version);
			confirming = false;
		} catch (e) {
			app.fail(e);
		} finally {
			starting = false;
		}
	}

	function published(iso: string) {
		const d = new Date(iso);
		return isNaN(d.getTime()) ? '' : d.toLocaleDateString();
	}

	let progress = $derived.by(() => {
		if (updates.installing?.down || job?.state === 'restarting') {
			return 'Restarting into the new version. The page reloads by itself when it answers.';
		}
		switch (job?.state) {
			case 'downloading':
				return job.total > 0
					? `Downloading: ${bytes(job.done)} of ${bytes(job.total)}`
					: `Downloading: ${bytes(job.done)}`;
			case 'verifying':
				return 'Checking the download against the published checksums and trying it out…';
			case 'installing':
				return 'Backing up the database and putting the new version in place…';
		}
		return 'Starting…';
	});
</script>

<section class="card p-4" id="updates">
	<div class="mb-1 flex items-center justify-between gap-3">
		<h2 class="text-sm font-semibold">Updates</h2>
		<button class="btn btn-default btn-sm" onclick={check} disabled={checking || busy}>
			<Icon name="refresh" size={13} />
			{checking ? 'Checking…' : 'Check now'}
		</button>
	</div>

	{#if !status}
		<p class="text-xs" style="color: var(--text-muted)">Loading…</p>
	{:else}
		<p class="mb-3 text-xs" style="color: var(--text-muted)">
			Running <span class="font-mono">{status.current}</span>.
			{#if status.latest}
				Latest release <span class="font-mono">{status.latest}</span>.
			{/if}
			{#if status.checkedAt}
				Checked {relative(status.checkedAt)}.
			{:else}
				Not checked yet — this server asks GitHub shortly after it starts, then every six hours.
			{/if}
		</p>

		{#if status.checkError}
			<p class="mb-3 text-xs" style="color: var(--danger)">
				Could not reach GitHub: {status.checkError}
			</p>
		{/if}

		{#if status.unsupported}
			<div
				class="mb-3 rounded-md px-3 py-2 text-xs"
				style="background: var(--warning-soft); color: var(--warning)"
			>
				This server cannot install updates itself: {status.unsupported}.
			</div>
		{/if}

		{#if busy}
			<div
				class="mb-3 rounded-md px-3 py-2.5 text-xs"
				style="background: var(--surface-2); color: var(--text-muted)"
			>
				<div class="mb-1.5 font-medium" style="color: var(--text)">
					Installing {updates.installing?.version ?? job?.version}
				</div>
				<div>{progress}</div>
				{#if job?.state === 'downloading' && job.total > 0}
					<div class="mt-2 h-1.5 overflow-hidden rounded-full" style="background: var(--surface-3)">
						<div
							class="h-full rounded-full transition-[width]"
							style="width: {Math.min(
								100,
								(job.done / job.total) * 100
							)}%; background: var(--primary)"
						></div>
					</div>
				{/if}
			</div>
		{:else if updates.stalled}
			<div
				class="mb-3 rounded-md px-3 py-2 text-xs"
				style="background: var(--danger-soft); color: var(--danger)"
			>
				The new version was put in place, but the server has not answered as it for three minutes.
				Check <span class="font-mono">journalctl -u wgui -n 50</span> on the server. The previous
				binary is kept beside the new one as <span class="font-mono">wgui.previous</span>, and the
				database as it was before the update as
				<span class="font-mono">wgui.db.before-{status.latest}</span>.
			</div>
		{:else if job?.state === 'failed'}
			<div
				class="mb-3 rounded-md px-3 py-2 text-xs"
				style="background: var(--danger-soft); color: var(--danger)"
			>
				{job.version} was not installed: {job.error}. Nothing was changed; this server is still
				running {status.current}.
			</div>
		{/if}

		{#if status.available && latest}
			<div class="rounded-md border" style="border-color: var(--border)">
				<div
					class="flex flex-wrap items-center justify-between gap-2 border-b px-3 py-2.5"
					style="border-color: var(--border)"
				>
					<div class="text-sm">
						<span class="font-semibold">{latest.version}</span> is available
						{#if status.releases.length > 1}
							<span class="text-xs" style="color: var(--text-muted)">
								— {status.releases.length} releases since yours, all listed below
							</span>
						{/if}
					</div>
					<button
						class="btn btn-primary btn-sm"
						onclick={() => (confirming = true)}
						disabled={busy || !!status.unsupported}
					>
						<Icon name="download" size={13} />
						Install {latest.version}
					</button>
				</div>

				<div class="max-h-[28rem] divide-y overflow-y-auto" style="border-color: var(--border)">
					{#each status.releases as release (release.version)}
						<article class="px-3 py-3" style="border-color: var(--border)">
							<header class="mb-2 flex flex-wrap items-baseline gap-x-2 text-xs">
								<span class="font-mono text-sm font-semibold" style="color: var(--text)"
									>{release.version}</span
								>
								{#if published(release.publishedAt)}
									<span style="color: var(--text-faint)">{published(release.publishedAt)}</span>
								{/if}
								<a
									href={release.url}
									target="_blank"
									rel="noopener noreferrer"
									class="ml-auto hover:underline"
									style="color: var(--primary)">On GitHub</a
								>
							</header>
							{#if release.notes}
								<Markdown source={release.notes} />
							{:else}
								<p class="text-xs" style="color: var(--text-faint)">
									No notes were published with this release.
								</p>
							{/if}
						</article>
					{/each}
				</div>
			</div>
		{:else if status.latest && status.current === status.latest}
			<p class="text-xs" style="color: var(--success)">This is the latest release.</p>
		{/if}
	{/if}
</section>

<Confirm
	open={confirming}
	title="Install {latest?.version}?"
	message="The release is downloaded, checked against its published checksums and tried out before anything changes. Then the database is backed up, the previous binary is kept beside the new one, and wgui restarts. Connected peers stay connected; the panel is unavailable for a few seconds."
	confirmLabel="Install"
	busy={starting}
	onconfirm={install}
	oncancel={() => (confirming = false)}
/>
