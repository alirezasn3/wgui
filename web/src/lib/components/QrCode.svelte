<script lang="ts">
	import { drawLabelledQR } from '$lib/qr';

	interface Props {
		value: string;
		size?: number;
		color?: string;
		topText?: string;
		bottomText?: string;
		caption?: string;
	}
	let { value, size = 240, color = '#023020', topText, bottomText, caption }: Props = $props();

	let canvas = $state<HTMLCanvasElement | null>(null);
	let error = $state('');

	$effect(() => {
		if (!canvas || !value) return;
		drawLabelledQR(canvas, { data: value, size, color, topText, bottomText, caption })
			.then(() => (error = ''))
			.catch((e) => (error = e instanceof Error ? e.message : String(e)));
	});
</script>

{#if error}
	<p class="text-xs" style="color: var(--danger)">Could not render the QR code: {error}</p>
{:else}
	<!-- Always on white: a code drawn on a dark surface will not scan. -->
	<canvas bind:this={canvas} class="rounded-md bg-white shadow-sm"></canvas>
{/if}
