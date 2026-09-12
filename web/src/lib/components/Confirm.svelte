<script lang="ts">
	import Modal from './Modal.svelte';

	interface Props {
		open: boolean;
		title: string;
		message: string;
		confirmLabel?: string;
		danger?: boolean;
		busy?: boolean;
		onconfirm: () => void;
		oncancel: () => void;
	}
	let {
		open,
		title,
		message,
		confirmLabel = 'Confirm',
		danger = false,
		busy = false,
		onconfirm,
		oncancel
	}: Props = $props();
</script>

<Modal {open} {title} width="26rem" onclose={oncancel}>
	<p class="text-sm leading-relaxed" style="color: var(--text-muted)">{message}</p>

	{#snippet footer()}
		<button class="btn btn-default" onclick={oncancel} disabled={busy}>Cancel</button>
		<button class="btn {danger ? 'btn-danger' : 'btn-primary'}" onclick={onconfirm} disabled={busy}>
			{busy ? 'Working…' : confirmLabel}
		</button>
	{/snippet}
</Modal>
