<script lang="ts">
	import { app } from '$lib/app.svelte';
	import Icon from './Icon.svelte';

	interface Props {
		value: string;
		label?: string;
		class?: string;
		title?: string;
	}
	let { value, label, class: className = 'btn btn-default btn-sm', title }: Props = $props();

	let copied = $state(false);

	async function copy() {
		try {
			// The clipboard API needs a secure context, which a self-signed
			// certificate does not always provide, so fall back to a hidden
			// textarea rather than failing silently.
			if (navigator.clipboard && window.isSecureContext) {
				await navigator.clipboard.writeText(value);
			} else {
				legacyCopy(value);
			}
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			app.fail(null, 'Could not copy to the clipboard');
		}
	}

	function legacyCopy(text: string) {
		const area = document.createElement('textarea');
		area.value = text;
		area.style.position = 'fixed';
		area.style.opacity = '0';
		document.body.appendChild(area);
		area.select();
		document.execCommand('copy');
		document.body.removeChild(area);
	}
</script>

<button class={className} onclick={copy} title={title ?? 'Copy'} aria-label={title ?? 'Copy'}>
	<Icon name={copied ? 'check' : 'copy'} size={14} />
	{#if label}{copied ? 'Copied' : label}{/if}
</button>
