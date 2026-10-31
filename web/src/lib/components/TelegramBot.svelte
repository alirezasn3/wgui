<script lang="ts">
	import { api } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import { bytesToGib, gibToBytes } from '$lib/format';
	import type { TelegramSettings, TelegramStatus } from '$lib/types';
	import { onMount } from 'svelte';
	import Confirm from './Confirm.svelte';

	// The thresholds and messages are part of the settings, so they are bound to
	// the page's copy and saved with its Save button. The token is not: it is
	// written the moment it is entered and never read back.
	let { telegram = $bindable() }: { telegram: TelegramSettings } = $props();

	let status = $state<TelegramStatus | null>(null);
	let statusError = $state('');
	let token = $state('');
	let saving = $state(false);
	let removing = $state(false);
	let confirmRemove = $state(false);
	let section = $state<HTMLElement | null>(null);

	onMount(() => {
		void refresh();
		// The page this is on appears after its settings have loaded, which is
		// after the browser has already tried to scroll to a link's #telegram.
		if (location.hash === '#telegram') section?.scrollIntoView();
		// Whether the bot is in touch with Telegram changes by itself.
		const timer = setInterval(() => void refresh(), 15000);
		return () => clearInterval(timer);
	});

	async function refresh() {
		try {
			status = await api.telegramStatus();
			statusError = '';
		} catch (e) {
			statusError = e instanceof Error ? e.message : String(e);
		}
	}

	async function saveToken() {
		saving = true;
		try {
			status = await api.setTelegramToken(token.trim());
			token = '';
			statusError = '';
			app.success(`Connected to @${status.username}`);
			// The bot reaches Telegram a moment after the token is stored, so
			// the first answer says it has not yet. Ask again rather than leave
			// that on the screen for the next scheduled refresh.
			setTimeout(() => void refresh(), 1500);
			setTimeout(() => void refresh(), 5000);
		} catch (e) {
			app.fail(e);
		} finally {
			saving = false;
		}
	}

	async function removeBot() {
		removing = true;
		try {
			status = await api.setTelegramToken('');
			confirmRemove = false;
			app.success('Telegram bot removed');
		} catch (e) {
			app.fail(e);
		} finally {
			removing = false;
		}
	}

	const messages = [
		{
			key: 'linked',
			label: 'When a customer connects',
			hint: 'Answers the link. {name} is the subscription.'
		},
		{
			key: 'invalid',
			label: 'When a link does not work',
			hint: 'Used, expired, or never made.'
		},
		{
			key: 'help',
			label: 'Anything else they send the bot',
			hint: 'Also what /start says with no link.'
		},
		{
			key: 'expiry',
			label: 'Expiry warning',
			hint: '{name}, {expires}, {days} and {hours} left.'
		},
		{
			key: 'usage',
			label: 'Low data warning',
			hint: '{name} and {remaining}, the data left.'
		},
		{
			key: 'list',
			label: 'What /list says about each subscription',
			hint: '{name}, {group}, {used}, {allowed}, {remaining}, {expires} and {days}. Used is the customer’s own; left is what will cut them off, the group’s for a group member. ∞ where there is no limit.',
			rows: 4
		},
		{
			key: 'listEmpty',
			label: 'What /list says when nothing is connected',
			hint: ''
		}
	] as const;
</script>

<section class="card p-4" id="telegram" bind:this={section}>
	<h2 class="mb-1 text-sm font-semibold">Telegram bot</h2>
	<p class="mb-3 text-xs" style="color: var(--text-muted)">
		Warns a customer in Telegram before their data or time runs out. Each subscription is connected
		to a chat from its own page, with a link the customer opens. The bot runs on the master; on a
		node this is carried there.
	</p>

	<!-- Status -->
	<div class="mb-4 rounded-md px-3 py-2.5 text-xs" style="background: var(--surface-2)">
		{#if statusError}
			<span style="color: var(--danger)">{statusError}</span>
		{:else if !status}
			<span style="color: var(--text-muted)">Checking…</span>
		{:else if !status.configured}
			<span style="color: var(--text-muted)"
				>No bot yet. Make one with <span class="font-mono">@BotFather</span> in Telegram and paste its
				token below.</span
			>
		{:else}
			<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
				<!-- No error yet and not running means it has not tried: a bot
				     that has failed always says why. -->
				<span
					class="inline-block h-1.5 w-1.5 rounded-full"
					style="background: {status.running
						? 'var(--success)'
						: status.error
							? 'var(--danger)'
							: 'var(--warning)'}"
				></span>
				<span class="font-medium" style="color: var(--text)">@{status.username}</span>
				<span style="color: var(--text-muted)">
					{status.running
						? 'in touch with Telegram'
						: status.error
							? 'not in touch with Telegram'
							: 'connecting to Telegram…'}
					· {status.linked} subscription{status.linked === 1 ? '' : 's'} connected
				</span>
			</div>
			{#if status.error}
				<p class="mt-1.5" style="color: var(--danger)">{status.error}</p>
			{/if}
		{/if}
	</div>

	<!-- Token -->
	<div class="mb-5">
		<label class="label" for="telegram-token">
			{status?.configured ? 'Replace the token' : 'Bot token'}
		</label>
		<div class="flex flex-wrap gap-2">
			<input
				id="telegram-token"
				class="field min-w-0 flex-1 font-mono text-xs"
				type="password"
				autocomplete="new-password"
				spellcheck="false"
				placeholder="123456:ABC-def…"
				bind:value={token}
				onkeydown={(e) => e.key === 'Enter' && token.trim() && !saving && saveToken()}
			/>
			<button class="btn btn-primary" onclick={saveToken} disabled={saving || !token.trim()}>
				{saving ? 'Checking…' : 'Save token'}
			</button>
			{#if status?.configured}
				<button class="btn btn-ghost" onclick={() => (confirmRemove = true)}>Remove</button>
			{/if}
		</div>
		<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
			Saved straight away, after Telegram confirms it. It is never shown again, and is not copied to
			nodes. This server has to reach <span class="font-mono">api.telegram.org</span>.
		</p>
	</div>

	<!-- Thresholds -->
	<div class="mb-1 grid gap-4 sm:grid-cols-2">
		<div>
			<label class="label" for="warn-days">Warn before expiry</label>
			<div class="relative">
				<input
					id="warn-days"
					class="field pr-24"
					type="number"
					min="0"
					max="365"
					bind:value={telegram.warnDays}
				/>
				<span
					class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
					style="color: var(--text-faint)">days or fewer</span
				>
			</div>
		</div>
		<div>
			<label class="label" for="warn-usage">Warn when data is low</label>
			<div class="relative">
				<!-- Edited in GiB, kept in bytes, and only converted when the field
				     is changed: a value set some other way is not rounded to two
				     decimals by saving. -->
				<input
					id="warn-usage"
					class="field pr-24"
					type="number"
					min="0"
					step="0.1"
					bind:value={
						() => bytesToGib(telegram.warnUsageBytes),
						(v) => (telegram.warnUsageBytes = gibToBytes(Number(v) || 0))
					}
				/>
				<span
					class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
					style="color: var(--text-faint)">GiB or less</span
				>
			</div>
		</div>
	</div>
	<p class="mb-4 text-[11px]" style="color: var(--text-faint)">
		0 turns a warning off. Each is sent once: renewing a subscription or resetting its usage arms it
		again. A group's members are warned about the group's limits, which are the ones that apply to
		them. Peers already cut off, or switched off by hand, are not warned.
	</p>

	<!-- Messages -->
	<details>
		<summary class="cursor-pointer text-xs font-medium" style="color: var(--text-muted)">
			What the bot says
		</summary>
		<div class="mt-3 space-y-3">
			<p class="text-[11px]" style="color: var(--text-faint)">
				Plain text, in whatever language your customers read. Customers can send the bot <span
					class="font-mono">/list</span
				>
				to see where their subscriptions stand. Write
				<span class="font-mono">{'{name}'}</span> where a subscription's name should go; each box
				says what else it can use. A link such as
				<span class="font-mono">https://t.me/youradmin</span> becomes tappable, so this is the place to
				say who to ask for a renewal. Leave a box empty to get its default back.
			</p>
			{#each messages as m (m.key)}
				<div>
					<label class="label" for="msg-{m.key}">{m.label}</label>
					<textarea
						id="msg-{m.key}"
						class="field text-sm"
						dir="auto"
						rows={'rows' in m ? m.rows : 2}
						bind:value={telegram.messages[m.key]}
					></textarea>
					{#if m.hint}
						<p class="mt-0.5 text-[11px]" style="color: var(--text-faint)">{m.hint}</p>
					{/if}
				</div>
			{/each}
		</div>
	</details>
</section>

<Confirm
	open={confirmRemove}
	title="Remove the Telegram bot?"
	message="The token is forgotten and warnings stop. Customers who connected stay connected, so putting the same bot back resumes where it left off."
	confirmLabel="Remove"
	danger
	busy={removing}
	onconfirm={removeBot}
	oncancel={() => (confirmRemove = false)}
/>
