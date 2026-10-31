<script lang="ts">
	import { api } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import type { PeerTelegram, TelegramLink } from '$lib/types';
	import CopyButton from './CopyButton.svelte';
	import Icon from './Icon.svelte';

	interface Props {
		peerId: string;
	}
	let { peerId }: Props = $props();

	let info = $state<PeerTelegram | null>(null);
	let link = $state<TelegramLink | null>(null);
	let loadError = $state('');
	let busy = $state(false);

	// The dialog's peer is a new object every time the list polls, and a prop
	// built from it reads as changed each time. A derived value only passes a
	// change on when it really differs, so everything below depends on this
	// rather than on the prop: an effect that re-ran on every poll would never
	// let a timer of a few seconds fire.
	let id = $derived(peerId);

	// Keyed on the id, not the peer: starting again from scratch on each poll
	// would throw away a link that has just been made.
	let loadedFor = $state<string | null>(null);
	$effect(() => {
		if (id === loadedFor) return;
		loadedFor = id;
		info = null;
		link = null;
		loadError = '';
		void load(id);
	});

	async function load(id: string) {
		try {
			const next = await api.peerTelegram(id);
			if (id === loadedFor) {
				info = next;
				loadError = '';
			}
		} catch (e) {
			if (id === loadedFor) loadError = e instanceof Error ? e.message : String(e);
		}
	}

	// While a link is out, notice when the customer uses it.
	let waiting = $derived(!!link && !info?.linked);
	$effect(() => {
		if (!waiting) return;
		const watching = id;
		const timer = setInterval(() => void load(watching), 4000);
		return () => clearInterval(timer);
	});

	async function makeLink() {
		busy = true;
		try {
			link = await api.linkPeerTelegram(peerId);
		} catch (e) {
			app.fail(e);
		} finally {
			busy = false;
		}
	}

	async function disconnect() {
		busy = true;
		try {
			await api.unlinkPeerTelegram(peerId);
			link = null;
			await load(peerId);
			app.success('Telegram disconnected');
		} catch (e) {
			app.fail(e);
		} finally {
			busy = false;
		}
	}

	let isAdmin = $derived(app.session?.isAdmin ?? false);
	let until = $derived(link ? new Date(link.expiresAt).toLocaleDateString() : '');
</script>

<!-- Nothing at all for someone who could do nothing about a bot that is not set
     up, rather than a block telling them so. -->
{#if loadError}
	<div>
		<div class="label">Telegram</div>
		<p class="text-xs" style="color: var(--text-faint)">{loadError}</p>
	</div>
{:else if info && (info.available || isAdmin)}
	<div>
		<div class="label mb-1">Telegram warnings</div>

		{#if !info.available}
			<p class="text-xs" style="color: var(--text-muted)">
				The bot is not set up yet.
				<a href="/settings#telegram" class="hover:underline" style="color: var(--primary)"
					>Add its token in Settings</a
				> to warn customers before they run out.
			</p>
		{:else}
			<p class="mb-2 text-xs" style="color: var(--text-muted)">
				{#if info.linked}
					<span style="color: var(--success)">Connected.</span> Warnings go to the customer's Telegram
					before this runs out of data or time.
				{:else if isAdmin}
					Not connected. Give the customer a link; when they open it in Telegram, they are warned
					before this runs out of data or time.
				{:else}
					Not connected. Ask an admin for a link.
				{/if}
			</p>

			{#if isAdmin && link && !info.linked}
				<div class="mb-2 space-y-2 rounded-md p-2.5" style="background: var(--surface-2)">
					<input
						class="field font-mono text-xs"
						readonly
						value={link.link}
						onfocus={(e) => e.currentTarget.select()}
						aria-label="Telegram link"
					/>
					<div class="flex flex-wrap gap-2">
						<CopyButton value={link.link} label="Copy link" />
						<a
							class="btn btn-default btn-sm"
							href={link.link}
							target="_blank"
							rel="noopener noreferrer"
						>
							<Icon name="share" size={13} /> Open in Telegram
						</a>
					</div>
					<p class="text-[11px]" style="color: var(--text-faint)">
						Works once, until {until}. Making another link cancels this one.
					</p>
				</div>
			{/if}

			<!-- Making a link is an admin's; stopping the warnings is open to whoever
			     may change the peer, and to the peer itself. -->
			<div class="flex flex-wrap gap-2">
				{#if isAdmin}
					<button class="btn btn-default btn-sm" onclick={makeLink} disabled={busy}>
						{info.linked ? 'Connect a different chat' : link ? 'Make a new link' : 'Get link'}
					</button>
				{/if}
				{#if info.linked}
					<button class="btn btn-ghost btn-sm" onclick={disconnect} disabled={busy}>
						Disconnect
					</button>
				{/if}
			</div>
		{/if}
	</div>
{/if}
