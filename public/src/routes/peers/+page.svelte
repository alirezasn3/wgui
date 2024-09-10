<script lang="ts">
	import { formatBytes, formatExpiry, sleep, type Group, type Peer } from '$lib'
	import { onMount } from 'svelte'
	import qr from 'qrcode'
	import { page } from '$app/stores'
	import { getContext } from 'svelte'
	import type { Writable } from 'svelte/store'
	import { goto } from '$app/navigation'

	const loading: Writable<boolean> = getContext('loading')
	const role: Writable<string> = getContext('role')
	const lastPageURL: Writable<URL | undefined> = getContext('lastPageURL')

	let peer: Peer | null = null
	let serverPublicKey = ''
	let endpoints: string[] = []
	let telegramBotID = ''
	let selectedEndpoint = ''
	let editing = false
	let newName = ''
	let newAllowedUsage = 0
	let newExpiresAt = 0
	let newPreferredEndpoint = ''
	let newRole = ''
	let expiresAtChanged = false
	let error = ''
	let config = ''
	let showSSI = false
	let showAddToGroupPanel = false
	let groups: Group[] = []
	let group: Group | null = null

	$: config = `[Interface]\nPrivateKey=${peer?.PrivateKey}\nAddress=${peer?.AllowedIPs}\nDNS=1.1.1.1,8.8.8.8\n[Peer]\nPublicKey=${serverPublicKey}\nAllowedIPs=0.0.0.0/0\nEndpoint=${selectedEndpoint}`

	onMount(async () => {
		try {
			let res = await fetch('/api/config')
			const configData = await res.json()
			serverPublicKey = configData.serverPublicKey
			endpoints = configData.endpoints
			telegramBotID = configData.telegramBotID
			selectedEndpoint = endpoints[0]
			const id = $page.url.searchParams.get('id')
			if (!id) return
			res = await fetch('/api/peers/' + encodeURIComponent(id))
			peer = await res.json()
			if (peer?.GroupID !== '000000000000000000000000') {
				res = await fetch('/api/groups/' + peer?.GroupID)
				group = await res.json()
			}
			while (!document.getElementById('canvas')) {
				await sleep(100)
			}
			qr.toCanvas(document.getElementById('canvas'), config, {
				width: document.body.clientWidth - 32 < 768 ? document.body.clientWidth - 32 : 768 - 32,
				color: { dark: '#023020' }
			})
			loading.set(false)
			while (true) {
				try {
					if (editing || showAddToGroupPanel) {
						await sleep(1000)
						continue
					}
					res = await fetch('/api/peers/' + encodeURIComponent(id))
					if (res.status === 200) {
						peer = await res.json()
						$loading = false
					} else {
						console.log(res.statusText)
					}
				} catch (error) {
					console.log(error)
				}
				await sleep(1000)
			}
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		}
	})

	async function deletePeer() {
		try {
			error = ''

			if (!peer) return

			if (!window.confirm(`Delete ${peer?.Name}?`)) return

			loading.set(true)

			const res = await fetch('/api/peers/' + encodeURIComponent(peer.ID), {
				method: 'DELETE'
			})
			if (res.status === 200) {
				await goto('/peers/all')
			} else {
				error = res.statusText
			}
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}

	async function editPeer() {
		try {
			error = ''

			if (!peer) return

			loading.set(true)

			const res = await fetch('/api/peers/' + encodeURIComponent(peer.ID), {
				method: 'PATCH',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({
					name: peer.Name !== newName ? newName : undefined,
					allowedUsage:
						peer.AllowedUsage / 1024000000 !== newAllowedUsage
							? newAllowedUsage * 1024000000
							: undefined,
					expiresAt: expiresAtChanged ? Date.now() + newExpiresAt * 24 * 3600 * 1000 : undefined,
					role: peer.Role !== newRole ? newRole : undefined,
					preferredEndpoint:
						peer.PreferredEndpoint !== newPreferredEndpoint ? newPreferredEndpoint : undefined
				})
			})
			if (res.status === 200) {
				if (peer.Name !== newName) peer.Name = newName
				editing = false
			} else {
				error = res.statusText
			}
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}

	function dataURLtoFile(dataurl: string, filename: string, type: string) {
		let arr = dataurl.split(',')
		let bstr = atob(arr[arr.length - 1])
		let n = bstr.length
		let u8arr = new Uint8Array(n)
		while (n--) {
			u8arr[n] = bstr.charCodeAt(n)
		}
		return new File([u8arr], filename, { type })
	}

	async function sharePeer(withTelegramBotLink = false, noImage = false) {
		try {
			error = ''
			if (!peer) return
			const canvas = document.createElement('canvas')
			canvas.width = canvas.clientWidth * 2
			canvas.height = canvas.clientHeight * 2
			const ctx = canvas.getContext('2d')
			if (ctx === null) return
			ctx.scale(2, 2)
			await qr.toCanvas(canvas, config, {
				width: 720,
				color: { dark: '#023020' }
			})
			ctx.font = '16px Roboto Mono'
			ctx.fillStyle = '#023020'
			const nameWidth = ctx.measureText(peer.Name).width
			const allowedIPsWidth = ctx.measureText(peer.AllowedIPs).width
			ctx.fillText(peer.Name, Math.round(360 - nameWidth / 2), 16)
			ctx.fillText(peer.AllowedIPs, Math.round(360 - allowedIPsWidth / 2), 716)
			const dataurl = canvas.toDataURL('image/png', 1)
			await navigator.share({
				title: peer?.Name,
				files: noImage
					? undefined
					: [dataURLtoFile(dataurl, `${peer?.Name.replaceAll('-', '')}.png`, 'image/png')],
				url: withTelegramBotLink
					? `https://t.me/${telegramBotID}?start=${btoa(peer.PublicKey)}`
					: undefined
			})
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		}
	}

	async function downloadPeer() {
		try {
			error = ''
			const file = new Blob([config], { type: 'application/octet-stream' })
			const a = document.createElement('a')
			a.href = URL.createObjectURL(file)
			a.download = peer?.Name.replaceAll('-', '') + '.conf'
			a.click()
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		}
	}

	async function copyPeer() {
		try {
			error = ''
			await navigator.clipboard.writeText(config)
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		}
	}

	async function resetPeerUsage() {
		try {
			error = ''

			if (!peer) return

			loading.set(true)

			const res = await fetch('/api/peers/' + encodeURIComponent(peer.ID), {
				method: 'PUT'
			})

			if (res.status !== 200) error = res.statusText
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}

	async function resetPeerExpiry() {
		try {
			error = ''

			if (!peer) return

			loading.set(true)

			const res = await fetch('/api/peers/' + encodeURIComponent(peer.ID), {
				method: 'PATCH',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({
					expiresAt: Date.now() + 30 * 24 * 3600 * 1000
				})
			})

			if (res.status !== 200) error = res.statusText
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	}

	async function loadGroups() {
		try {
			const res = await fetch('/api/groups')
			if (res.status === 200) groups = await res.json()
			else error = res.statusText
		} catch (error) {
			console.log(error)
			error = String(error)
		} finally {
			loading.set(false)
		}
	}

	async function addPeerToGroup(groupID: string, groupName: string) {
		try {
			if (peer === null) return
			if (peer.GroupID == '000000000000000000000000') {
				if (!window.confirm(`Add ${peer?.Name} to ${groupName}? Peer's usage will be reset!`))
					return
			} else {
				if (
					!window.confirm(`Change ${peer?.Name} group to ${groupName}? Peer's usage will be reset!`)
				)
					return
			}
			loading.set(true)
			const res = await fetch(`/api/groups/${groupID}/${encodeURIComponent(peer.ID)}`, {
				method: 'PUT'
			})
			if (res.status === 200) location.reload()
			else error = res.statusText
			while (!document.getElementById('canvas')) {
				await sleep(100)
			}
			qr.toCanvas(document.getElementById('canvas'), config, {
				width: document.body.clientWidth - 32 < 768 ? document.body.clientWidth - 32 : 768 - 32,
				color: { dark: '#023020' }
			})
		} catch (error) {
			console.log(error)
			error = String(error)
		} finally {
			loading.set(false)
		}
	}
</script>

{#if peer !== null}
	{#if showAddToGroupPanel}
		<div>
			<button
				class="mb-8"
				on:click={async () => {
					showAddToGroupPanel = false
					while (!document.getElementById('canvas')) {
						await sleep(100)
					}
					qr.toCanvas(document.getElementById('canvas'), config, {
						width: document.body.clientWidth - 32 < 768 ? document.body.clientWidth - 32 : 768 - 32,
						color: { dark: '#023020' }
					})
				}}
			>
				<span
					class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
				>
					close
				</span>
			</button>
			<div class="mb-4">Add {peer.Name} to :</div>
			<div class="grid grid-cols-1 gap-2">
				{#each groups as group}
					<button
						disabled={group.ID === peer.GroupID}
						class="w-full rounded border border-neutral-800 px-4 py-2 text-left {group.ID ===
							peer.GroupID && 'bg-neutral-800'}"
						on:click={() => addPeerToGroup(group.ID, group.Name)}
					>
						{group.Name} | {group.PeerIDs.length} peers {group.ID === peer.GroupID
							? '| current'
							: ''}
					</button>
				{/each}
			</div>
		</div>
	{:else}
		<div class="min-h-svh w-full">
			{#if $lastPageURL?.pathname.includes('groups') && $lastPageURL.searchParams.get('id') !== null}
				<a
					href={'/groups?id=' + $lastPageURL.searchParams.get('id')}
					class="mb-4 block flex w-fit items-center rounded-full border border-neutral-800 p-2 pr-4 hover:cursor-pointer hover:bg-neutral-950"
				>
					<span class="material-symbols-outlined mr-2"> arrow_left </span>
					<span>Back to Group</span>
				</a>
			{/if}
			{#if !editing}
				<div class="mb-4 grid w-fit grid-cols-5 gap-2 md:grid-cols-9">
					{#if $role !== 'user'}
						<button
							on:click={() => {
								editing = true
								if (peer) {
									expiresAtChanged = false
									newName = peer.Name
									newAllowedUsage = peer.AllowedUsage / 1024000000
									newExpiresAt = +((peer.ExpiresAt - Date.now()) / 1000 / 3600 / 24).toFixed(2)
									newPreferredEndpoint = peer.PreferredEndpoint
									newRole = peer.Role
								}
							}}
						>
							<span
								class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
							>
								edit
							</span>
						</button>
						<button on:click={deletePeer}>
							<span
								class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
							>
								delete
							</span>
						</button>
						{#if peer.GroupID === '000000000000000000000000'}
							<button on:click={resetPeerUsage}>
								<span
									class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
								>
									restart_alt
								</span>
							</button>
							<button on:click={resetPeerExpiry} class="relative">
								<span
									class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
								>
									history
								</span>
								<div class="absolute right-0 top-0 text-xs font-thin">30</div>
							</button>
						{/if}
						<button
							on:click={() => {
								loading.set(true)
								showAddToGroupPanel = true
								loadGroups()
							}}
						>
							<span
								class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
							>
								groups
							</span>
						</button>
					{/if}
					<button on:click={downloadPeer}>
						<span
							class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
						>
							download
						</span>
					</button>
					<button on:click={() => sharePeer()} class="relative">
						<span
							class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
						>
							share
						</span>
						<div class="absolute -right-2 top-0 text-xs font-thin tracking-tighter">qrc</div>
					</button>
					{#if $role === 'admin'}
						<button on:click={() => sharePeer(true, true)} class="relative">
							<span
								class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
							>
								share
							</span>
							<div class="absolute -right-2 top-0 text-xs font-thin tracking-tighter">url</div>
						</button>
						<button on:click={() => sharePeer(true)} class="relative">
							<span
								class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
							>
								share
							</span>
							<div class="absolute -right-2 top-0 text-[10px] font-thin tracking-tighter">full</div>
						</button>
					{/if}
				</div>
			{/if}
			{#if error}
				<div class="mb-2 text-red-900">{error}</div>
			{/if}
			{#if editing}
				<div class="mb-2 w-full max-w-lg">
					<input
						bind:value={newName}
						class="w-full rounded border border-neutral-800 bg-neutral-950 px-4 py-2 text-neutral-50 outline-none"
						type="text"
						autocomplete="off"
						placeholder="Name"
					/>
				</div>
				<div class="mb-2 w-full max-w-lg">
					<input
						disabled={true}
						bind:value={newPreferredEndpoint}
						class="hidden w-full rounded border border-neutral-800 bg-neutral-950 px-4 py-2 text-neutral-50 outline-none"
						type="text"
						placeholder="Preferred Endpoint"
					/>
				</div>
				{#if peer.GroupID === '000000000000000000000000'}
					<div class="mb-2 flex w-full max-w-lg">
						<input
							bind:value={newAllowedUsage}
							class="w-full rounded-l border-y border-l border-neutral-800 bg-neutral-950 px-4 py-2 text-neutral-50 outline-none"
							type="number"
							placeholder="Allowed Usage"
						/>
						<div
							class="flex items-center rounded-r border-y border-r border-neutral-800 bg-neutral-950 pr-2 text-neutral-50"
						>
							GB
						</div>
					</div>
					<div class="mb-2 flex w-full max-w-lg">
						<input
							on:change={() => (expiresAtChanged = true)}
							bind:value={newExpiresAt}
							class="w-full rounded-l border-y border-l border-neutral-800 bg-neutral-950 px-4 py-2 text-neutral-50 outline-none"
							type="number"
							placeholder="Expiry"
						/>
						<div
							class="flex items-center rounded-r border-y border-r border-neutral-800 bg-neutral-950 pr-2 text-neutral-50"
						>
							Days
						</div>
					</div>
				{/if}
				{#if $role === 'admin'}
					<div class="mb-2 flex">
						<select
							bind:value={newRole}
							class="w-full max-w-lg rounded border border-neutral-800 bg-neutral-900 px-4 py-2 outline-none"
						>
							<option value="user"> User </option>
							<option value="distributor"> Distributor </option>
							<option value="admin"> Admin </option>
						</select>
					</div>
				{/if}
				<div class="mb-4 grid w-full max-w-lg grid-cols-2 gap-2">
					<button
						on:click={editPeer}
						class="rounded bg-neutral-50 px-4 py-2 text-lg font-bold text-neutral-950 hover:bg-neutral-300"
						>SAVE</button
					>
					<button
						on:click={() => {
							editing = false
							error = ''
						}}
						class="rounded bg-neutral-50 px-4 py-2 text-lg font-bold text-neutral-950 hover:bg-neutral-300"
						>CANCEL</button
					>
				</div>
			{:else}
				<div class="mb-2 flex items-center">
					<div class="text-lg font-bold">{peer.Name}</div>
					<div class="ml-1">{'('}{peer.Role}{')'}</div>
				</div>
				{#if $role !== 'user'}
					<div class="mb-2 flex items-center">
						<div>{peer.AllowedIPs}</div>
					</div>
				{/if}
				<div class="mb-2">{formatExpiry(peer.ExpiresAt)}</div>
				<div class="mb-2">
					{formatBytes(peer.TotalTX + peer.TotalRX)} / {formatBytes(peer.AllowedUsage)}
				</div>
				<div class="mb-2 flex">
					<div class="mr-2 flex">
						<span class="material-symbols-outlined mr-1"> arrow_upward </span>
						<div>
							{formatBytes(peer.TotalTX)}
						</div>
					</div>
					<div class="flex">
						<span class="material-symbols-outlined mr-1"> arrow_downward </span>
						<div>
							{formatBytes(peer.TotalRX)}
						</div>
					</div>
				</div>
			{/if}
			{#if peer.PreferredEndpoint}
				<div class="mb-2">{peer.PreferredEndpoint}</div>
			{/if}
			{#if !editing}
				<div class="mb-2 {peer.TelegramChatID === 0 ? 'text-red-900' : 'text-blue-900'}">
					Telegram Bot {peer.TelegramChatID === 0 ? 'Not Activated' : 'Activated'}
				</div>
				{#if peer.GroupID !== '000000000000000000000000'}
					<div class="mb-2">
						Group:
						<a href={'/groups?id=' + group?.ID} class="text-yellow-900">
							{group?.Name}
						</a>
					</div>
				{/if}
				<select
					disabled={$role === 'user'}
					bind:value={selectedEndpoint}
					on:change={() =>
						qr.toCanvas(document.getElementById('canvas'), config, {
							width:
								document.body.clientWidth - 32 < 768 ? document.body.clientWidth - 32 : 768 - 32,
							color: { dark: '#023020' }
						})}
					class="mb-4 w-full max-w-lg rounded border border-neutral-800 bg-neutral-900 px-4 py-2 outline-none"
				>
					{#each endpoints as e}
						<option value={e}>{e}</option>
					{/each}
				</select>
				{#if $role !== 'user'}
					<div
						class="mb-4 overflow-hidden rounded border border-neutral-800 px-4 py-2 transition-all {showSSI
							? 'max-h-[1000px]'
							: 'max-h-10'}"
					>
						<button on:click={() => (showSSI = !showSSI)} class="flex items-center pb-2">
							<span class="material-symbols-outlined mr-1 transition-all {showSSI && 'rotate-180'}">
								arrow_drop_down
							</span>
							<span>{showSSI ? 'HIDE' : 'SHOW'} SSIs</span>
						</button>
						<div class="mb-4 grid grid-cols-1 gap-2 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
							{#each peer.ServerSpecificInfo as ssi}
								<div class="rounded border border-neutral-800 px-4 py-2">
									<div class="font-bold">{ssi.Address}</div>
									<div class="flex">
										<div class="mr-1">Endpoint:</div>
										<div>
											{!ssi.Endpoint || ssi.Endpoint === '<nil>' ? 'unknown' : ssi.Endpoint}
										</div>
									</div>
									<div class="flex">
										<div class="mr-1">Last Handshake:</div>
										<div>
											{ssi.LastHandshakeTime || 'unknown'}
										</div>
									</div>
									<div class="flex">
										<div class="mr-2 flex">
											<span class="material-symbols-outlined mr-1"> arrow_upward </span>
											<div>
												{formatBytes(ssi.CurrentTX)}
											</div>
										</div>
										<div class="flex">
											<span class="material-symbols-outlined mr-1"> arrow_downward </span>
											<div>
												{formatBytes(ssi.CurrentRX)}
											</div>
										</div>
									</div>
								</div>
							{/each}
						</div>
					</div>
				{/if}
			{/if}
			<canvas id="canvas" class="mb-4 rounded"></canvas>
			{#if $role !== 'user'}
				<div class="relative max-w-[736px] rounded bg-neutral-800 p-4 shadow-inner">
					<!-- svelte-ignore a11y-click-events-have-key-events -->
					<!-- svelte-ignore a11y-no-static-element-interactions -->
					<span
						on:click={copyPeer}
						class="material-symbols-outlined absolute right-2 top-2 text-neutral-50 hover:cursor-pointer"
					>
						content_copy
					</span>
					<div class="text-teal-600">[Interface]</div>
					<div class="overflow-hidden text-ellipsis">
						<span class="text-purple-500">PrivateKey = </span>
						<span class="text-orange-500">
							{peer.PrivateKey}
						</span>
					</div>
					<div>
						<span class="text-purple-500">Address = </span>
						<span class="text-blue-500">
							{peer.AllowedIPs}
						</span>
					</div>
					<div>
						<span class="text-purple-500">DNS = </span>
						<span class="text-blue-500"> 1.1.1.1,8.8.8.8 </span>
					</div>
					<div class="text-teal-600">[Peer]</div>
					<div class="overflow-hidden text-ellipsis">
						<span class="text-purple-500">PublicKey = </span>
						<span class="text-orange-500">
							{serverPublicKey}
						</span>
					</div>
					<div>
						<span class="text-purple-500">AllowedIPs = </span>
						<span class="text-blue-500"> 0.0.0.0/0 </span>
					</div>
					<div>
						<span class="text-purple-500">Endpoint = </span>
						<span class="text-blue-500">
							{selectedEndpoint}
						</span>
					</div>
				</div>
			{/if}
		</div>
	{/if}
{/if}
