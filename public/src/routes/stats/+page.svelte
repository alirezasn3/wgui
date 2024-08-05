<script lang="ts">
	import type { Peer } from '$lib'
	import { onMount } from 'svelte'
	import { getContext } from 'svelte'
	import type { Writable } from 'svelte/store'
	import Chart, { Legend } from 'chart.js/auto'

	const loading: Writable<boolean> = getContext('loading')

	let canvas: HTMLCanvasElement
	let error = ''
	let isps: any = {}

	let totalCount = 0
	let doneCount = 0
	let percent = '0%'
	$: percent = ((doneCount / totalCount) * 100).toFixed() + '%'

	function fetchWithCount(url: string) {
		return new Promise<{ asName: string }>(async (resolve, reject) => {
			const res = await fetch(url)
			if (res.status !== 200) return reject(res.statusText)
			const data: { asName: string } = await res.json()
			doneCount++
			return resolve(data)
		})
	}

	onMount(async () => {
		try {
			loading.set(true)

			const res = await fetch('/api/peers')
			if (res.status === 200) {
				const d = await res.json()
				const peers: Peer[] = Object.values(d.peers)
				const requests = []
				for (const peer of peers) {
					for (const ssi of peer.serverSpecificInfo) {
						if (ssi.endpoint === '<nil>') continue
						requests.push(
							'https://ipee-api.alirezasn.workers.dev/v1/info/' + ssi.endpoint.split(':')[0]
						)
						totalCount++
					}
				}
				const data = await Promise.allSettled(requests.map((url) => fetchWithCount(url)))

				for (const d of data) {
					if (d.status === 'fulfilled') {
						if (isps[d.value.asName]) isps[d.value.asName]++
						else isps[d.value.asName] = 1
					}
				}

				new Chart(canvas, {
					type: 'doughnut',
					options: {
						responsive: true,
						plugins: {
							legend: {
								position: document.body.clientWidth < 1200 ? 'bottom' : 'chartArea'
							}
						}
					},
					data: {
						labels: Object.keys(isps),
						datasets: [
							{
								label: `Peers`,
								data: Object.values(isps),
								hoverOffset: 4
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

<div class="relative">
	{#if percent !== '100%'}
		<div
			class="absolute left-0 top-0 flex h-[calc(100svh-96px)] w-full items-center justify-center"
		>
			<div class="pt-4">
				{percent}
			</div>
		</div>
	{/if}
	<canvas bind:this={canvas} class="max-h-[calc(100svh-96px)] w-full rounded bg-neutral-900 p-4"
	></canvas>
</div>
