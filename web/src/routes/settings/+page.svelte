<script lang="ts">
	import { api } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import LimitInput from '$lib/components/LimitInput.svelte';
	import QrCode from '$lib/components/QrCode.svelte';
	import SystemTuning from '$lib/components/SystemTuning.svelte';
	import Updates from '$lib/components/Updates.svelte';
	import { bytesToGib, gibToBytes } from '$lib/format';
	import type { Settings } from '$lib/types';
	import { onMount } from 'svelte';

	let settings = $state<Settings | null>(null);
	let endpointsText = $state('');
	let defaultUsageGib = $state(0);
	let groupUsageGib = $state(0);
	let loadError = $state('');
	let saving = $state(false);

	onMount(load);

	async function load() {
		try {
			const s = await api.settings();
			settings = s;
			endpointsText = s.endpoints.join('\n');
			defaultUsageGib = bytesToGib(s.peerDefaults.allowedUsageBytes);
			groupUsageGib = bytesToGib(s.groupDefaults.allowedUsageBytes);
			loadError = '';
		} catch (e) {
			loadError = e instanceof Error ? e.message : String(e);
		}
	}

	async function save() {
		if (!settings) return;
		saving = true;
		try {
			const payload: Settings = {
				...settings,
				endpoints: endpointsText
					.split('\n')
					.map((line) => line.trim())
					.filter(Boolean),
				peerDefaults: {
					...settings.peerDefaults,
					allowedUsageBytes: gibToBytes(defaultUsageGib)
				},
				groupDefaults: {
					...settings.groupDefaults,
					allowedUsageBytes: gibToBytes(groupUsageGib)
				}
			};
			const saved = await api.saveSettings(payload);
			settings = saved;
			endpointsText = saved.endpoints.join('\n');
			// The server normalises what it stores, so reflect that back rather
			// than leaving the form showing values that were not kept.
			defaultUsageGib = bytesToGib(saved.peerDefaults.allowedUsageBytes);
			groupUsageGib = bytesToGib(saved.groupDefaults.allowedUsageBytes);
			app.success('Settings saved');
			await app.loadSession();
		} catch (e) {
			app.fail(e);
		} finally {
			saving = false;
		}
	}

	// The default endpoint has to be one of the endpoints on offer.
	let endpointOptions = $derived(
		endpointsText
			.split('\n')
			.map((line) => line.trim())
			.filter(Boolean)
	);
</script>

<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
	<div>
		<h1 class="text-xl font-semibold">Settings</h1>
		<p class="text-xs" style="color: var(--text-muted)">
			Takes effect immediately — no restart needed
		</p>
	</div>
	<button class="btn btn-primary" onclick={save} disabled={saving || !settings}>
		{saving ? 'Saving…' : 'Save changes'}
	</button>
</div>

{#if loadError}
	<div class="card p-4 text-sm" style="border-color: var(--danger); color: var(--danger)">
		{loadError}
	</div>
{:else if settings}
	<div class="grid max-w-3xl gap-4">
		<Updates />

		<section class="card p-4">
			<h2 class="mb-3 text-sm font-semibold">Server</h2>
			<div class="space-y-4">
				<div>
					<label class="label" for="public-address">Public address</label>
					<input
						id="public-address"
						class="field"
						bind:value={settings.publicAddress}
						placeholder="vpn.example.com"
						autocomplete="off"
					/>
					<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
						The hostname clients reach this server on. Used when no endpoint is configured.
					</p>
				</div>

				<div>
					<label class="label" for="endpoints">Endpoints</label>
					<textarea
						id="endpoints"
						class="field font-mono text-xs"
						rows="4"
						bind:value={endpointsText}
						placeholder={'de.example.com:51820\nnl.example.com:51820'}
					></textarea>
					<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
						One <span class="font-mono">host:port</span> per line. A peer can be pinned to any of these
						in its own settings.
					</p>
				</div>

				<div>
					<label class="label" for="default-endpoint">Default endpoint</label>
					<select id="default-endpoint" class="field" bind:value={settings.defaultEndpoint}>
						<option value="">First in the list</option>
						{#each endpointOptions as endpoint (endpoint)}
							<option value={endpoint}>{endpoint}</option>
						{/each}
					</select>
				</div>
			</div>
		</section>

		<section class="card p-4">
			<h2 class="mb-1 text-sm font-semibold">Defaults for new peers</h2>
			<p class="mb-3 text-xs" style="color: var(--text-muted)">
				Pre-filled in the create form and applied when a field is left out.
			</p>
			<div class="grid gap-4 sm:grid-cols-2">
				<div>
					<label class="label" for="default-usage">Data allowance</label>
					<LimitInput
						id="default-usage"
						value={defaultUsageGib}
						unit="GiB"
						onchange={(v) => (defaultUsageGib = v)}
					/>
				</div>
				<div>
					<label class="label" for="default-expiry">Expires in</label>
					<LimitInput
						id="default-expiry"
						value={settings.peerDefaults.expiryDays}
						unit="days"
						unlimitedLabel="Never"
						onchange={(v) => settings && (settings.peerDefaults.expiryDays = v)}
					/>
				</div>
				<div>
					<label class="label" for="default-role">Role</label>
					<select id="default-role" class="field" bind:value={settings.peerDefaults.role}>
						<option value="user">User</option>
						<option value="distributor">Distributor</option>
						<option value="admin">Admin</option>
					</select>
				</div>
				<div>
					<label class="label" for="default-dns">DNS</label>
					<input
						id="default-dns"
						class="field"
						bind:value={settings.peerDefaults.dns}
						placeholder="1.1.1.1, 8.8.8.8"
						autocomplete="off"
					/>
				</div>
				<div>
					<label class="label" for="default-allowed">Allowed IPs</label>
					<input
						id="default-allowed"
						class="field font-mono text-xs"
						bind:value={settings.peerDefaults.allowedIPs}
						placeholder="0.0.0.0/0"
						autocomplete="off"
					/>
					<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
						What the client routes through the tunnel.
					</p>
				</div>
				<div class="grid grid-cols-2 gap-3">
					<div>
						<label class="label" for="default-mtu">MTU</label>
						<input
							id="default-mtu"
							class="field"
							type="number"
							min="0"
							max="9000"
							bind:value={settings.peerDefaults.mtu}
							placeholder="0"
						/>
					</div>
					<div>
						<label class="label" for="default-keepalive">Keepalive</label>
						<input
							id="default-keepalive"
							class="field"
							type="number"
							min="0"
							bind:value={settings.peerDefaults.persistentKeepalive}
							placeholder="0"
						/>
					</div>
				</div>
			</div>
			<p class="mt-2 text-[11px]" style="color: var(--text-faint)">
				MTU and keepalive are left out of the generated configuration when set to 0.
			</p>
		</section>

		<section class="card p-4">
			<h2 class="mb-3 text-sm font-semibold">Defaults for new groups</h2>
			<div class="grid gap-4 sm:grid-cols-2">
				<div>
					<label class="label" for="group-usage">Shared allowance</label>
					<LimitInput
						id="group-usage"
						value={groupUsageGib}
						unit="GiB"
						onchange={(v) => (groupUsageGib = v)}
					/>
				</div>
				<div>
					<label class="label" for="group-expiry">Expires in</label>
					<LimitInput
						id="group-expiry"
						value={settings.groupDefaults.expiryDays}
						unit="days"
						unlimitedLabel="Never"
						onchange={(v) => settings && (settings.groupDefaults.expiryDays = v)}
					/>
				</div>
			</div>
		</section>

		<section class="card p-4">
			<h2 class="mb-1 text-sm font-semibold">QR codes</h2>
			<p class="mb-3 text-xs" style="color: var(--text-muted)">
				How a peer's code looks when it is downloaded or shared. The labels are drawn into the
				image, so a screenshot still says who it belongs to.
			</p>
			<div class="grid gap-4 sm:grid-cols-[1fr_auto]">
				<div class="space-y-4">
					<div>
						<label class="label" for="qr-color">Colour</label>
						<div class="flex items-center gap-2">
							<input
								id="qr-color"
								class="h-9 w-12 shrink-0 cursor-pointer rounded-md border bg-transparent p-1"
								style="border-color: var(--border-strong)"
								type="color"
								bind:value={settings.qr.color}
							/>
							<input
								class="field font-mono text-sm"
								bind:value={settings.qr.color}
								spellcheck="false"
							/>
						</div>
						<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
							Keep it dark. A light code on white does not scan.
						</p>
					</div>

					<div>
						<label class="label" for="qr-caption">Caption</label>
						<input
							id="qr-caption"
							class="field"
							bind:value={settings.qr.caption}
							placeholder="e.g. support@example.com"
							maxlength="64"
							autocomplete="off"
						/>
						<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
							An optional line under the address, printed on every code.
						</p>
					</div>

					<div class="space-y-2">
						<label class="flex cursor-pointer items-center gap-2 text-sm">
							<input
								type="checkbox"
								class="accent-[var(--primary)]"
								bind:checked={settings.qr.showName}
							/>
							Print the peer's name above the code
						</label>
						<label class="flex cursor-pointer items-center gap-2 text-sm">
							<input
								type="checkbox"
								class="accent-[var(--primary)]"
								bind:checked={settings.qr.showAddress}
							/>
							Print its tunnel address below
						</label>
					</div>
				</div>

				<div class="flex flex-col items-center gap-1">
					<QrCode
						value="[Interface]\nPrivateKey = preview\nAddress = 10.0.0.2/32"
						size={190}
						color={settings.qr.color}
						topText={settings.qr.showName ? 'anna-laptop' : undefined}
						bottomText={settings.qr.showAddress ? '10.0.0.2/32' : undefined}
						caption={settings.qr.caption || undefined}
					/>
					<span class="text-[11px]" style="color: var(--text-faint)">Preview</span>
				</div>
			</div>
		</section>

		<section class="card p-4">
			<h2 class="mb-1 text-sm font-semibold">Endpoint lookups</h2>
			<p class="mb-3 text-xs" style="color: var(--text-muted)">
				Resolves the ISP, organisation and ASN behind a peer's address. Only ever runs when you
				press the button on a peer, and answers are cached.
			</p>
			<div class="space-y-4">
				<label class="flex cursor-pointer items-center gap-2 text-sm">
					<input
						type="checkbox"
						class="accent-[var(--primary)]"
						bind:checked={settings.ipinfo.enabled}
					/>
					Enabled
				</label>
				<div>
					<label class="label" for="ipinfo-url">Service URL</label>
					<input
						id="ipinfo-url"
						class="field font-mono text-xs"
						bind:value={settings.ipinfo.baseURL}
						autocomplete="off"
					/>
					<p class="mt-1 text-[11px]" style="color: var(--text-faint)">
						The address is appended to this URL.
					</p>
				</div>
				<div class="grid gap-4 sm:grid-cols-[1fr_auto]">
					<div>
						<label class="label" for="ipinfo-key">API key</label>
						<input
							id="ipinfo-key"
							class="field font-mono text-xs"
							bind:value={settings.ipinfo.apiKey}
							placeholder="leave empty if the service needs none"
							autocomplete="off"
						/>
					</div>
					<div class="sm:w-44">
						<label class="label" for="ipinfo-key-header">Sent as</label>
						<input
							id="ipinfo-key-header"
							class="field font-mono text-xs"
							bind:value={settings.ipinfo.apiKeyHeader}
							placeholder="Authorization"
							autocomplete="off"
						/>
					</div>
				</div>
				<p class="-mt-2 text-[11px]" style="color: var(--text-faint)">
					The key is sent verbatim as that header, so write
					<span class="font-mono">Bearer abc123</span> if the service expects the prefix.
				</p>

				<div class="max-w-[12rem]">
					<label class="label" for="ipinfo-ttl">Cache for</label>
					<div class="relative">
						<input
							id="ipinfo-ttl"
							class="field pr-14"
							type="number"
							min="1"
							bind:value={settings.ipinfo.cacheTTLHours}
						/>
						<span
							class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
							style="color: var(--text-faint)">hours</span
						>
					</div>
				</div>
			</div>
		</section>

		<section class="card p-4">
			<h2 class="mb-3 text-sm font-semibold">Accounting</h2>
			<div class="max-w-[12rem]">
				<label class="label" for="flush">Write usage every</label>
				<div class="relative">
					<input
						id="flush"
						class="field pr-16"
						type="number"
						min="1"
						max="300"
						bind:value={settings.usageFlushSeconds}
					/>
					<span
						class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
						style="color: var(--text-faint)">seconds</span
					>
				</div>
			</div>
			<p class="mt-2 text-[11px]" style="color: var(--text-faint)">
				Traffic is measured every second either way; this only controls how often it is written to
				disk. Quota enforcement runs on the same interval, so a lower value cuts peers off sooner at
				the cost of more writes.
			</p>
		</section>

		<SystemTuning />

		<div class="flex justify-end gap-2 pb-4">
			<button class="btn btn-default" onclick={load} disabled={saving}>
				<Icon name="refresh" size={14} /> Discard changes
			</button>
			<button class="btn btn-primary" onclick={save} disabled={saving}>
				{saving ? 'Saving…' : 'Save changes'}
			</button>
		</div>
	</div>
{/if}
