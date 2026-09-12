<script lang="ts">
	import '../app.css';
	import { page } from '$app/state';
	import { app } from '$lib/app.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import Toasts from '$lib/components/Toasts.svelte';
	import { onMount } from 'svelte';

	let { children } = $props();

	let menuOpen = $state(false);

	onMount(() => {
		app.loadTheme();
		void app.loadSession();
	});

	// A plain user has nothing to administer, so it only gets its own page.
	let links = $derived(
		app.session?.canWrite
			? [
					{ href: '/', label: 'Dashboard', icon: 'dashboard' as const },
					{ href: '/peers', label: 'Peers', icon: 'peers' as const },
					{ href: '/groups', label: 'Groups', icon: 'groups' as const },
					...(app.session?.isAdmin
						? [
								{ href: '/automation', label: 'Automation', icon: 'power' as const },
								{ href: '/nodes', label: 'Nodes', icon: 'globe' as const },
								{ href: '/settings', label: 'Settings', icon: 'settings' as const }
							]
						: [])
				]
			: [{ href: '/peers', label: 'My connection', icon: 'peers' as const }]
	);

	let current = $derived(page.url.pathname.replace(/\/$/, '') || '/');

	function isActive(href: string) {
		return href === '/' ? current === '/' : current.startsWith(href);
	}
</script>

<svelte:head><title>WGUI</title></svelte:head>

<div class="min-h-svh" style="background: var(--bg)">
	{#if app.loading}
		<div class="flex min-h-svh items-center justify-center">
			<div
				class="h-8 w-8 animate-spin rounded-full border-2 border-transparent"
				style="border-top-color: var(--primary); border-right-color: var(--primary)"
			></div>
		</div>
	{:else if !app.session}
		<div class="flex min-h-svh items-center justify-center p-6">
			<div class="card max-w-md p-6 text-center">
				<div class="mb-3 flex justify-center" style="color: var(--danger)">
					<Icon name="info" size={28} />
				</div>
				<h1 class="mb-2 text-lg font-semibold">Not connected</h1>
				<p class="text-sm leading-relaxed" style="color: var(--text-muted)">
					{app.sessionError}
				</p>
				<button class="btn btn-primary mt-4" onclick={() => app.loadSession()}>
					<Icon name="refresh" size={14} /> Try again
				</button>
			</div>
		</div>
	{:else}
		<!-- Sidebar -->
		<aside
			class="fixed inset-y-0 left-0 z-40 flex w-56 flex-col border-r transition-transform lg:translate-x-0"
			class:translate-x-0={menuOpen}
			class:-translate-x-full={!menuOpen}
			style="background: var(--surface); border-color: var(--border)"
		>
			<div class="flex h-14 items-center gap-2 px-4">
				<div
					class="flex h-7 w-7 items-center justify-center rounded-md text-xs font-bold text-white"
					style="background: var(--primary)"
				>
					W
				</div>
				<span class="text-sm font-semibold tracking-wide">WGUI</span>
			</div>

			<nav class="flex-1 space-y-0.5 px-2 py-2">
				{#each links as link (link.href)}
					<a
						href={link.href}
						onclick={() => (menuOpen = false)}
						class="flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors"
						style={isActive(link.href)
							? 'background: var(--primary-soft); color: var(--primary)'
							: 'color: var(--text-muted)'}
					>
						<Icon name={link.icon} size={16} />
						{link.label}
					</a>
				{/each}
			</nav>

			<div class="border-t px-3 py-3" style="border-color: var(--border)">
				<div class="mb-2 min-w-0">
					<div class="truncate text-sm font-medium">{app.session.name}</div>
					<div class="text-[11px] capitalize" style="color: var(--text-faint)">
						{app.session.role}
					</div>
				</div>
				<button
					class="btn btn-ghost btn-sm w-full justify-start"
					onclick={() => app.setTheme(app.theme === 'dark' ? 'light' : 'dark')}
				>
					<Icon name={app.theme === 'dark' ? 'sun' : 'moon'} size={14} />
					{app.theme === 'dark' ? 'Light theme' : 'Dark theme'}
				</button>
				<div class="mt-2 px-3 text-[10px]" style="color: var(--text-faint)">
					wgui {app.session.version}
				</div>
			</div>
		</aside>

		{#if menuOpen}
			<div
				class="fixed inset-0 z-30 bg-black/50 lg:hidden"
				role="button"
				tabindex="-1"
				aria-label="Close menu"
				onclick={() => (menuOpen = false)}
				onkeydown={(e) => e.key === 'Escape' && (menuOpen = false)}
			></div>
		{/if}

		<div class="lg:pl-56">
			<header
				class="sticky top-0 z-20 flex h-14 items-center gap-3 border-b px-4 lg:hidden"
				style="background: var(--surface); border-color: var(--border)"
			>
				<button class="btn btn-ghost btn-icon" onclick={() => (menuOpen = true)} aria-label="Menu">
					<Icon name="menu" size={18} />
				</button>
				<span class="text-sm font-semibold">WGUI</span>
			</header>

			<main class="p-4 sm:p-6">
				{#if app.session.isNode}
					<!-- On every page, not just Nodes: the confusing case is editing a
					     peer here and watching the change vanish a few seconds later. -->
					<div
						class="mb-4 flex flex-wrap items-center gap-2 rounded-lg px-3 py-2 text-xs"
						style="background: var(--surface-2); border: 1px solid var(--warning); color: var(--text-muted)"
					>
						<span style="color: var(--warning)"><Icon name="info" size={14} /></span>
						<span>
							<strong style="color: var(--warning)">Node.</strong> Peers come from
							<span class="font-mono">{app.session.masterUrl}</span>. Changes made here are sent
							there and come straight back, so the master must be reachable to edit anything.
						</span>
					</div>
				{/if}
				{@render children()}
			</main>
		</div>
	{/if}

	<Toasts />
</div>
