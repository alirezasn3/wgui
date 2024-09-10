<!-- <script lang="ts">
	import { htmlLegendPlugin, type Peer } from '$lib'
	import { onMount } from 'svelte'
	import { getContext } from 'svelte'
	import type { Writable } from 'svelte/store'
	import Chart from 'chart.js/auto'
	import protobuf from 'protobufjs'

	const loading: Writable<boolean> = getContext('loading')

	let canvas: HTMLCanvasElement
	let error = ''
	let isps: any = {}

	let totalCount = 0
	let doneCount = 0
	let percent = '0%'
	$: percent = ((doneCount / totalCount) * 100).toFixed() + '%'

	function fetchWithCount(url: string) {
		return new Promise<{ asName: string; organizationName: string }>(async (resolve, reject) => {
			const res = await fetch(url)
			if (res.status !== 200) return reject(res.statusText)
			const data: { asName: string; organizationName: string } = await res.json()
			doneCount++
			return resolve(data)
		})
	}

	onMount(async () => {
		try {
			const res = await fetch('/api/peers')
			if (res.status === 200) {
				const pb = await protobuf.load('/Peer.proto')
				const PBPeers = pb.lookupType('PBPeers')
				const ab = await res.arrayBuffer()
				const peers: Peer[] = PBPeers.decode(new Uint8Array(ab), ab.byteLength).toJSON().Peers
				const requests = []
				for (const peer of peers) {
					for (const ssi of peer.ServerSpecificInfo) {
						if (ssi.Endpoint === '<nil>') continue
						requests.push(
							'https://ipee-api.alirezasn.workers.dev/v1/info/' + ssi.Endpoint.split(':')[0]
						)
						totalCount++
					}
				}
				const data = await Promise.allSettled(requests.map((url) => fetchWithCount(url)))

				for (const d of data) {
					if (d.status === 'fulfilled') {
						if (isps[d.value.asName + ' ' + d.value.organizationName])
							isps[d.value.asName + ' ' + d.value.organizationName]++
						else isps[d.value.asName + ' ' + d.value.organizationName] = 1
					}
				}

				const ch = new Chart(canvas, {
					type: 'doughnut',
					options: {
						responsive: true,
						plugins: {
							legend: {
								display: false
							},
							// @ts-ignore
							htmlLegend: {
								containerID: 'legend'
							}
						}
					},
					// @ts-ignore
					plugins: [htmlLegendPlugin],
					data: {
						labels: Object.keys(isps),
						datasets: [
							{
								label: `Peers`,
								data: Object.values(isps),
								hoverOffset: 8
							}
						]
					}
				})

				percent = '100%'
			}
		} catch (e) {
			console.log(e)
			error = (e as Error).message
		} finally {
			loading.set(false)
		}
	})
</script>

<div class="relative flex max-xl:flex-col">
	{#if percent !== '100%'}
		<div
			class="absolute left-0 top-0 flex h-[calc(100svh-96px)] w-full items-center justify-center"
		>
			<div class="pt-4">
				{percent}
			</div>
		</div>
	{/if}
	<canvas
		bind:this={canvas}
		class="max-h-[calc(100svh-96px)] w-full max-w-3xl rounded bg-neutral-900 p-4 max-xl:mb-4 xl:mr-4"
	></canvas>
	<div id="legend"></div>
</div> -->

<div></div>
