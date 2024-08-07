<script lang="ts">
	import { formatBytes, formatExpiry, sleep, type Peer, type ServerSpecificInfo } from '$lib'
	import { onMount } from 'svelte'
	import { fly } from 'svelte/transition'
	import qr from 'qrcode'
	import { page } from '$app/stores'
	import { getContext } from 'svelte'
	import type { Writable } from 'svelte/store'

	const loading: Writable<boolean> = getContext('loading')
	const search: Writable<string> = getContext('search')
	const role: Writable<string> = getContext('role')

	let peers: Peer[] = []
	let currentPeer: Peer | null = null
	let serverPublicKey = ''
	let serverAddress = ''
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
	let servers: ServerSpecificInfo[] = []
	let showSSI = false
	let combinedUsage = ''

	;(async () => {
		let t
		let res: Response
		let data
		while (true) {
			if ($page.url.pathname !== '/') {
				await sleep(1000)
				continue
			}
			t = Date.now()
			try {
				if (currentPeer === null) {
					res = await fetch('/api/peers')
					if (res.status === 200) {
						data = await res.json()
						peers = Object.values(data.peers as { string: Peer }).sort(
							(a, b) => a.expiresAt - b.expiresAt
						)
						$role = data.role

						const tempServers: Record<string, ServerSpecificInfo> = {}

						for (const peer of peers) {
							for (const ssi of peer.serverSpecificInfo) {
								if (tempServers[ssi.address]) {
									tempServers[ssi.address].currentRX += ssi.currentRX
									tempServers[ssi.address].currentTX += ssi.currentTX
								} else {
									tempServers[ssi.address] = {
										address: ssi.address,
										currentTX: ssi.currentTX,
										currentRX: ssi.currentRX,
										lastHandshakeTime: '',
										endpoint: ''
									}
								}
							}
						}

						servers = Object.values<ServerSpecificInfo>(tempServers)
					} else {
						console.log(res.statusText)
					}
				} else {
					res = await fetch('/api/peers/' + encodeURIComponent(currentPeer._id))
					if (res.status === 200) {
						if (currentPeer) currentPeer = await res.json()
					} else {
						console.log(res.statusText)
					}
				}
			} catch (error) {
				console.log(error)
			}
			await sleep(1000)
		}
	})()

	$: config = `[Interface]\nPrivateKey=${currentPeer?.privateKey}\nAddress=${currentPeer?.allowedIPs}\nDNS=1.1.1.1,8.8.8.8\n[Peer]\nPublicKey=${serverPublicKey}\nAllowedIPs=0.0.0.0/0\nEndpoint=${selectedEndpoint}`

	$: combinedUsage = formatBytes(
		peers.reduce((previous: number, current: Peer) => {
			if (
				current.name.toLowerCase().includes($search.toLocaleLowerCase()) ||
				current.allowedIPs.includes($search)
			)
				return previous + current.totalRX + current.totalTX
			return previous
		}, 0)
	)

	onMount(async () => {
		try {
			loading.set(true)
			let res = await fetch('/api/config')
			const data = await res.json()
			serverPublicKey = data.serverPublicKey
			serverAddress = data.serverAddress
			endpoints = data.endpoints
			telegramBotID = data.telegramBotID
			selectedEndpoint = endpoints[0]
			const peer = $page.url.searchParams.get('peer')
			if (peer) {
				const res = await fetch('/api/peers/' + encodeURIComponent(peer))
				currentPeer = await res.json()
				while (!document.getElementById('canvas')) {
					await sleep(100)
				}
				qr.toCanvas(document.getElementById('canvas'), config, {
					width: document.body.clientWidth - 32 < 768 ? document.body.clientWidth - 32 : 768 - 32,
					color: { dark: '#023020' }
				})
			}
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	})

	async function deletePeer() {
		try {
			error = ''

			if (!currentPeer) return

			if (!window.confirm(`Delete ${currentPeer?.name}?`)) return

			loading.set(true)

			const res = await fetch('/api/peers/' + encodeURIComponent(currentPeer._id), {
				method: 'DELETE'
			})
			if (res.status === 200) {
				currentPeer = null
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

	async function editPeer() {
		try {
			error = ''

			if (!currentPeer) return

			loading.set(true)

			const res = await fetch('/api/peers/' + encodeURIComponent(currentPeer._id), {
				method: 'PATCH',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({
					name: currentPeer.name !== newName ? newName : undefined,
					allowedUsage:
						currentPeer.allowedUsage / 1024000000 !== newAllowedUsage
							? newAllowedUsage * 1024000000
							: undefined,
					expiresAt: expiresAtChanged ? Date.now() + newExpiresAt * 24 * 3600 * 1000 : undefined,
					role: currentPeer.role !== newRole ? newRole : undefined,
					preferredEndpoint:
						currentPeer.preferredEndpoint !== newPreferredEndpoint
							? newPreferredEndpoint
							: undefined
				})
			})
			if (res.status === 200) {
				if (currentPeer.name !== newName) currentPeer.name = newName
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

	async function sharePeer(withTelegramBotLink = false) {
		try {
			error = ''
			const dataurl = await qr.toDataURL(document.createElement('canvas'), config, {
				width: 720,
				color: { dark: '#023020' }
			})
			if (withTelegramBotLink) {
				if (!currentPeer) return
				await navigator.share({
					title: currentPeer?.name,
					files: [
						dataURLtoFile(dataurl, `${currentPeer?.name.replaceAll('-', '')}.png`, 'image/png')
					],
					url: `https://t.me/${telegramBotID}?start=${btoa(currentPeer.publicKey)}`
				})
			} else {
				await navigator.share({
					title: currentPeer?.name,
					files: [
						dataURLtoFile(dataurl, `${currentPeer?.name.replaceAll('-', '')}.png`, 'image/png')
					]
				})
			}
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
			a.download = currentPeer?.name.replaceAll('-', '') + '.conf'
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

			if (!currentPeer) return

			loading.set(true)

			const res = await fetch('/api/peers/' + encodeURIComponent(currentPeer._id), {
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

			if (!currentPeer) return

			loading.set(true)

			const res = await fetch('/api/peers/' + encodeURIComponent(currentPeer._id), {
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
</script>

{#if $role !== 'user'}
	<div class="relative mb-2 flex items-center">
		<input
			class="w-full rounded border border-neutral-800 bg-neutral-950 px-4 py-2 text-lg outline-none"
			bind:value={$search}
			placeholder="Search Peers"
			type="text"
			autocomplete="off"
		/>
		{#if $search.length}
			<button on:click={() => ($search = '')} class="absolute right-2 flex items-center">
				<span class="material-symbols-outlined"> close </span>
			</button>
		{/if}
	</div>
	{#if $role === 'admin' && $search.length === 0}
		<div
			class="mb-2 overflow-hidden rounded border border-neutral-800 px-4 py-2 transition-all {showSSI
				? 'max-h-[1000px]'
				: 'max-h-10'}"
		>
			<button on:click={() => (showSSI = !showSSI)} class="flex items-center">
				<span class="material-symbols-outlined mr-1 transition-all {showSSI && 'rotate-180'}">
					arrow_drop_down
				</span>
				<span>{showSSI ? 'HIDE' : 'SHOW'} SSIs</span>
			</button>
			<div class="mt-2 grid grid-cols-1 gap-2 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
				{#each servers as server}
					<div class="rounded border border-neutral-800 px-4 py-2">
						<div class="font-bold">
							{server?.address}
						</div>
						<div class="flex">
							<div class="mr-2 flex">
								<span class="material-symbols-outlined mr-1"> arrow_upward </span>
								<div>
									{formatBytes(server?.currentTX)}
								</div>
							</div>
							<div class="flex">
								<span class="material-symbols-outlined mr-1"> arrow_downward </span>
								<div>
									{formatBytes(server?.currentRX)}
								</div>
							</div>
						</div>
					</div>
				{/each}
			</div>
		</div>
	{/if}
	{#if $search.length === 0}
		<div
			class="mb-2 flex items-center rounded border border-neutral-800 px-4 py-2 text-xs md:text-sm"
		>
			<div class="flex items-center">
				<div class="mr-2 h-2 w-2 rounded-full bg-blue-900"></div>
				No Usage
			</div>
			<div class="mx-4 flex items-center">
				<div class="mr-2 h-2 w-2 rounded-full bg-red-800"></div>
				Expired
			</div>
			<div class="flex items-center">
				<div class="mr-2 h-2 w-2 rounded-full bg-yellow-700"></div>
				Usage Limit Reached
			</div>
		</div>
	{/if}
	{#if $search.length > 0}
		<div class="mb-2 rounded border border-neutral-800 px-4 py-2">
			Combined Usage: {combinedUsage}
		</div>{/if}
{/if}

{#if currentPeer !== null}
	<div
		transition:fly={{ duration: 100, y: 100 }}
		class="absolute left-0 top-0 min-h-svh w-full rounded bg-neutral-900 p-4"
	>
		<div class="rtl mb-2">
			<button
				on:click={() => {
					currentPeer = null
					editing = false
				}}
				class="rounded-full bg-neutral-50 hover:bg-neutral-300"
			>
				<span class="material-symbols-outlined full rounded-full p-2 invert hover:cursor-pointer">
					close
				</span>
			</button>
		</div>
		{#if !editing}
			<div class="rtl ml-auto grid w-fit grid-cols-4 gap-2">
				{#if $role !== 'user'}
					<button
						on:click={() => {
							editing = true
							if (currentPeer) {
								expiresAtChanged = false
								newName = currentPeer.name
								newAllowedUsage = currentPeer.allowedUsage / 1024000000
								newExpiresAt = +((currentPeer.expiresAt - Date.now()) / 1000 / 3600 / 24).toFixed(2)
								newPreferredEndpoint = currentPeer.preferredEndpoint
								newRole = currentPeer.role
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
				<button on:click={downloadPeer}>
					<span
						class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
					>
						download
					</span>
				</button>
				<button on:click={() => sharePeer()}>
					<span
						class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
					>
						share
					</span>
				</button>
				{#if $role === 'admin'}
					<button on:click={() => sharePeer(true)} class="relative">
						<span
							class="material-symbols-outlined rounded-full border border-neutral-800 p-2 hover:cursor-pointer hover:bg-neutral-950"
						>
							share
						</span>
						<div class="absolute -right-2 top-0 text-xs font-thin tracking-tighter">bot</div>
					</button>
				{/if}
			</div>
		{/if}
		<div class="my-4 h-[1px] bg-neutral-800"></div>
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
				<div class="text-lg font-bold">{currentPeer.name}</div>
				<div class="ml-1">{'('}{currentPeer.role}{')'}</div>
			</div>
			{#if $role !== 'user'}
				<div class="mb-2 flex items-center">
					<div>{currentPeer.allowedIPs}</div>
				</div>
			{/if}
			<div class="mb-2">{formatExpiry(currentPeer.expiresAt)}</div>
			<div class="mb-2">
				{formatBytes(currentPeer.totalTX + currentPeer.totalRX)} / {formatBytes(
					currentPeer.allowedUsage
				)}
			</div>
			<div class="mb-2 flex">
				<div class="mr-2 flex">
					<span class="material-symbols-outlined mr-1"> arrow_upward </span>
					<div>
						{formatBytes(currentPeer.totalTX)}
					</div>
				</div>
				<div class="flex">
					<span class="material-symbols-outlined mr-1"> arrow_downward </span>
					<div>
						{formatBytes(currentPeer.totalRX)}
					</div>
				</div>
			</div>
		{/if}
		{#if currentPeer.preferredEndpoint}
			<div class="mb-2">{currentPeer.preferredEndpoint}</div>
		{/if}
		{#if !editing}
			<select
				disabled={$role === 'user'}
				bind:value={selectedEndpoint}
				on:change={() =>
					qr.toCanvas(document.getElementById('canvas'), config, {
						width: document.body.clientWidth - 32 < 768 ? document.body.clientWidth - 32 : 768 - 32,
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
						{#each currentPeer.serverSpecificInfo as ssi}
							<div class="rounded border border-neutral-800 px-4 py-2">
								<div class="font-bold">{ssi.address}</div>
								<div class="flex">
									<div class="mr-1">Endpoint:</div>
									<div>
										{!ssi.endpoint || ssi.endpoint === '<nil>' ? 'unknown' : ssi.endpoint}
									</div>
								</div>
								<div class="flex">
									<div class="mr-1">Last Handshake:</div>
									<div>
										{ssi.lastHandshakeTime || 'unknown'}
									</div>
								</div>
								<div class="flex">
									<div class="mr-2 flex">
										<span class="material-symbols-outlined mr-1"> arrow_upward </span>
										<div>
											{formatBytes(ssi.currentTX)}
										</div>
									</div>
									<div class="flex">
										<span class="material-symbols-outlined mr-1"> arrow_downward </span>
										<div>
											{formatBytes(ssi.currentRX)}
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
						{currentPeer.privateKey}
					</span>
				</div>
				<div>
					<span class="text-purple-500">Address = </span>
					<span class="text-blue-500">
						{currentPeer.allowedIPs}
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
{:else}
	<div class="w-full max-w-full overflow-x-auto bg-neutral-950">
		<table class="w-full text-sm md:text-base">
			<thead class="bg-neutral-900 text-left">
				<th class="px-2 py-2">#</th>
				<th class="px-2 py-2">Name</th>
				<th class="px-2 py-2">Expiry</th>
				<th class="px-2 py-2">Usage</th>
			</thead>
			<tbody>
				{#each peers.filter((p) => !search || p.name
							.toLowerCase()
							.includes($search.toLowerCase()) || p.allowedIPs.includes($search)) as peer, i}
					<tr
						on:click={async () => {
							currentPeer = peer
							newName = peer.name
							newAllowedUsage = peer.allowedUsage / 1024000000
							newExpiresAt = +((peer.expiresAt - Date.now()) / 1000 / 3600 / 24).toFixed(2)
							newPreferredEndpoint = peer.preferredEndpoint
							newRole = peer.role
							while (!document.getElementById('canvas')) {
								await sleep(100)
							}
							qr.toCanvas(document.getElementById('canvas'), config, {
								width:
									document.body.clientWidth - 32 < 768 ? document.body.clientWidth - 32 : 768 - 32,
								color: { dark: '#023020' }
							})
						}}
						class="{peer.disabled && peer.totalRX + peer.totalTX >= peer.allowedUsage
							? 'bg-yellow-700 hover:bg-yellow-800'
							: peer.disabled
								? 'bg-red-800 hover:bg-red-900'
								: !peer.disabled && peer.totalRX + peer.totalTX === 0
									? 'bg-blue-900 hover:bg-blue-800'
									: 'bg-neutral-900 hover:bg-neutral-800'} border-neutral-800 text-left odd:border-y hover:cursor-pointer"
					>
						<td class="px-2 py-1">{i + 1}</td>
						<td class="whitespace-nowrap px-2 py-1">{peer.name}</td>
						<td class="whitespace-nowrap px-2 py-1">{formatExpiry(peer.expiresAt)}</td>
						<td class="whitespace-nowrap px-2 py-1"
							>{formatBytes(peer.totalTX + peer.totalRX)}/{formatBytes(peer.allowedUsage)}</td
						>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
