// place files you want to import through the `$lib` alias in this folder.

export interface ServerSpecificInfo {
	address: string
	lastHandshakeTime: string
	endpoint: string
	currentTX: number
	currentRX: number
}

export interface Peer {
	_id: string
	role: string
	name: string
	preferredEndpoint: string
	allowedIPs: string
	publicKey: string
	privateKey: string
	disabled: boolean
	allowedUsage: number
	expiresAt: number
	endpoint: string
	lastHandshakeTime: string
	totalTX: number
	totalRX: number
	serverSpecificInfo: ServerSpecificInfo[]
	telegramChatID: number
}

export interface Log {
	publicAddress: string
	time: number
	level: string
	msg: string
	peer: string
}

export interface Device {
	name: string
	listenPort: number
	peers: {
		PublicKey: string
		PresharedKey: string
		Endpoint: { IP: string }
		PersistentKeepaliveInterval: string
		LastHandshakeTime: string
		ReceiveBytes: number
		TransmitBytes: number
		AllowedIPs: { IP: string }[]
		ProtocolVersion: number
	}[]
}

export const formatExpiry = (expiresAt: number, noPrefix = false) => {
	if (!expiresAt) return 'unknown'
	let totalSeconds = Math.trunc((expiresAt - Date.now()) / 1000)
	const prefix = totalSeconds < 0 && !noPrefix ? '-' : ''
	totalSeconds = Math.abs(totalSeconds)
	if (totalSeconds / 60 < 1) return `${prefix}${totalSeconds} seconds`
	const totalMinutes = Math.trunc(totalSeconds / 60)
	if (totalMinutes / 60 < 1) return `${prefix}${totalMinutes} minutes`
	const totalHours = Math.trunc(totalMinutes / 60)
	if (totalHours / 24 < 1) return `${prefix}${totalHours} hours`
	return `${prefix}${Math.trunc(totalHours / 24)} days`
}

export const formatBytes = (totalBytes: number, space = true) => {
	if (!totalBytes) return `00.00${space ? ' ' : ''}KB`
	const totalKilos = totalBytes / 1024
	const totalMegas = totalKilos / 1000
	const totalGigas = totalMegas / 1000
	const totalTeras = totalGigas / 1000
	if (totalKilos < 100)
		return `${totalKilos < 10 ? '0' : ''}${totalKilos.toFixed(2)}${space ? ' ' : ''}KB`
	if (totalMegas < 100)
		return `${totalMegas < 10 ? '0' : ''}${totalMegas.toFixed(2)}${space ? ' ' : ''}MB`
	if (totalGigas < 100)
		return `${totalGigas < 10 ? '0' : ''}${totalGigas.toFixed(2)}${space ? ' ' : ''}GB`
	return `${totalTeras < 10 ? '0' : ''}${totalTeras.toFixed(2)}${space ? ' ' : ''}TB`
}

export const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

export const htmlLegendPlugin = {
	id: 'htmlLegend',
	afterUpdate(
		chart: {
			data: { datasets: { data: { [x: string]: any } }[] }
			options: { plugins: { legend: { labels: { generateLabels: (arg0: any) => any } } } }
			config: { type: any }
			toggleDataVisibility: (arg0: any) => void
			setDatasetVisibility: (arg0: any, arg1: boolean) => void
			isDatasetVisible: (arg0: any) => any
			update: () => void
		},
		args: any,
		options: { containerID: string }
	) {
		const container = document.getElementById(options.containerID)
		if (!container) return
		container.innerHTML = ''
		const items = chart.options.plugins.legend.labels.generateLabels(chart)
		items.forEach(
			(item: {
				index: any
				datasetIndex: any
				fillStyle: string
				strokeStyle: string
				lineWidth: string
				fontColor: string
				hidden: any
				text: string
			}) => {
				const div = document.createElement('div')
				div.className = 'flex mb-2 hover:cursor-pointer'
				div.onclick = () => {
					const { type } = chart.config
					if (type === 'pie' || type === 'doughnut') {
						chart.toggleDataVisibility(item.index)
					} else {
						chart.setDatasetVisibility(
							item.datasetIndex,
							!chart.isDatasetVisible(item.datasetIndex)
						)
					}
					chart.update()
				}
				const box = document.createElement('div')
				box.style.backgroundColor = item.fillStyle
				box.className = `w-6 h-6 mr-3 rounded-full`
				const text = document.createElement('div')
				text.style.color = item.fontColor
				text.style.textDecoration = item.hidden ? 'line-through' : ''
				text.innerText = `${item.text} (${chart.data.datasets[0].data[item.index]})`
				div.appendChild(box)
				div.appendChild(text)
				container.appendChild(div)
			}
		)
	}
}
