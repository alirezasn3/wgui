// place files you want to import through the `$lib` alias in this folder.

export interface ServerSpecificInfo {
	address: string
	lastHandshake: number
	endpoint: string
	tx: number
	rx: number
}

export interface Peer {
	role: string
	name: string
	allowedIPs: string
	publicKey: string
	privateKey: string
	disabled: boolean
	allowedUsage: number
	expiresAt: number
	totalTX: number
	totalRX: number
	serverSpecificInfo: ServerSpecificInfo[]
	telegramChatID: number
	groupName: string
}

export interface Group {
	name: string
	peers: string[]
	allowedUsage: number
	totalTX: number
	totalRX: number
	expiresAt: number
	disabled: boolean
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

