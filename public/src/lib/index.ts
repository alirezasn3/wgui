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
