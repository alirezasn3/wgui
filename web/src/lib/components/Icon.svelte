<script lang="ts">
	// A small inline icon set, so the panel does not depend on a webfont that
	// would have to be fetched over the tunnel.
	interface Props {
		name: keyof typeof paths;
		size?: number;
		class?: string;
	}
	let { name, size = 16, class: className = '' }: Props = $props();

	const paths = {
		dashboard: 'M3 3h7v8H3zM14 3h7v5h-7zM14 11h7v10h-7zM3 14h7v7H3z',
		peers: 'M16 11a4 4 0 10-8 0 4 4 0 008 0zM3 21a9 9 0 0118 0',
		groups:
			'M7 8a3 3 0 106 0 3 3 0 00-6 0zM2 20a6 6 0 0112 0M16 11a3 3 0 100-6M18 20h4a6 6 0 00-4-5.7',
		settings:
			'M12 15a3 3 0 100-6 3 3 0 000 6zM19.4 15a1.7 1.7 0 00.3 1.9l.1.1a2 2 0 11-2.8 2.8l-.1-.1a1.7 1.7 0 00-2.9 1.2V21a2 2 0 11-4 0v-.1A1.7 1.7 0 007 19.4a1.7 1.7 0 00-1.9.3l-.1.1a2 2 0 11-2.8-2.8l.1-.1a1.7 1.7 0 00-1.2-2.9H1a2 2 0 110-4h.1A1.7 1.7 0 002.6 7a1.7 1.7 0 00-.3-1.9l-.1-.1a2 2 0 112.8-2.8l.1.1a1.7 1.7 0 001.9.3H7a1.7 1.7 0 001-1.5V1a2 2 0 114 0v.1a1.7 1.7 0 001 1.5 1.7 1.7 0 001.9-.3l.1-.1a2 2 0 112.8 2.8l-.1.1a1.7 1.7 0 00-.3 1.9V7a1.7 1.7 0 001.5 1H21a2 2 0 110 4h-.1a1.7 1.7 0 00-1.5 1z',
		search: 'M11 19a8 8 0 100-16 8 8 0 000 16zM21 21l-4.3-4.3',
		close: 'M18 6L6 18M6 6l12 12',
		plus: 'M12 5v14M5 12h14',
		edit: 'M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7M18.5 2.5a2.1 2.1 0 013 3L12 15l-4 1 1-4z',
		trash:
			'M3 6h18M8 6V4a1 1 0 011-1h6a1 1 0 011 1v2M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6M10 11v6M14 11v6',
		qr: 'M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h3v3h-3zM18 18h3v3h-3z',
		copy: 'M9 9h10a1 1 0 011 1v10a1 1 0 01-1 1H9a1 1 0 01-1-1V10a1 1 0 011-1zM5 15H4a1 1 0 01-1-1V4a1 1 0 011-1h10a1 1 0 011 1v1',
		download: 'M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4M7 10l5 5 5-5M12 15V3',
		up: 'M12 19V5M5 12l7-7 7 7',
		down: 'M12 5v14M19 12l-7 7-7-7',
		sort: 'M8 9l4-4 4 4M8 15l4 4 4-4',
		check: 'M20 6L9 17l-5-5',
		power: 'M12 2v10M18.4 6.6a9 9 0 11-12.8 0',
		refresh: 'M21 12a9 9 0 11-2.6-6.4M21 3v6h-6',
		globe: 'M12 21a9 9 0 100-18 9 9 0 000 18zM3 12h18M12 3a15 15 0 010 18 15 15 0 010-18z',
		info: 'M12 21a9 9 0 100-18 9 9 0 000 18zM12 16v-4M12 8h.01',
		moon: 'M21 12.8A9 9 0 1111.2 3a7 7 0 009.8 9.8z',
		sun: 'M12 17a5 5 0 100-10 5 5 0 000 10zM12 1v2M12 21v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M1 12h2M21 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4',
		menu: 'M3 6h18M3 12h18M3 18h18',
		key: 'M14.5 9.5a4 4 0 10-4.2 4l1.7 1.7 2 .3.3 2 1.7 1.7 2.5-2.5-4-4z',
		filter: 'M3 4h18l-7 8v6l-4 2v-8z',
		chevronLeft: 'M15 18l-6-6 6-6',
		chevronRight: 'M9 18l6-6-6-6',
		share:
			'M18 8a3 3 0 100-6 3 3 0 000 6zM6 15a3 3 0 100-6 3 3 0 000 6zM18 22a3 3 0 100-6 3 3 0 000 6zM8.6 13.5l6.8 4M15.4 6.5l-6.8 4'
	} as const;
</script>

<svg
	width={size}
	height={size}
	viewBox="0 0 24 24"
	fill="none"
	stroke="currentColor"
	stroke-width="1.8"
	stroke-linecap="round"
	stroke-linejoin="round"
	class={className}
	aria-hidden="true"
>
	<path d={paths[name]} />
</svg>
