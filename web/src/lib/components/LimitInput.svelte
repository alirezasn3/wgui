<script lang="ts">
	/**
	 * A number field paired with an "unlimited" switch, used for both quota and
	 * expiry. Zero is what the API stores for "no limit".
	 */
	interface Props {
		value: number;
		unit: string;
		unlimitedLabel?: string;
		id?: string;
		min?: number;
		step?: number;
		/** Greys the whole control out, e.g. when a group governs this limit. */
		disabled?: boolean;
		onchange: (value: number) => void;
	}
	let {
		value,
		unit,
		unlimitedLabel = 'Unlimited',
		id,
		min = 0,
		step = 1,
		disabled = false,
		onchange
	}: Props = $props();

	// "Unlimited" is the checkbox, never an empty field. Deriving it from the
	// value meant that clearing the input to retype a number disabled the field
	// under the cursor and ticked the box.
	let unlimited = $state(false);

	// The text is held separately from the number so a half-typed value is not
	// reformatted or cleared while it is being typed.
	let text = $state('');

	// Remembering the last real entry means ticking "unlimited" and unticking it
	// does not silently wipe what the operator had typed.
	let remembered = $state(0);

	// Adopt the prop when the dialog is opened for something else. Starting at
	// null means this also does the initial setup.
	let lastValue = $state<number | null>(null);
	$effect(() => {
		if (value === lastValue) return;
		lastValue = value;
		if (value) remembered = value;
		// Only follow the prop's idea of "unlimited" when the change came from
		// outside; typing keeps its own state.
		unlimited = !value;
		text = value ? String(value) : '';
	});

	function onInput(raw: string) {
		text = raw;
		lastValue = null; // the next prop echo is ours, not a fresh peer

		const parsed = Number(raw);
		const next = raw.trim() === '' || Number.isNaN(parsed) ? 0 : Math.max(parsed, 0);
		lastValue = next;
		onchange(next);
	}

	function setUnlimited(on: boolean) {
		unlimited = on;
		if (on) {
			if (value) remembered = value;
			text = '';
			lastValue = 0;
			onchange(0);
		} else {
			const restored = remembered || step;
			text = String(restored);
			lastValue = restored;
			onchange(restored);
		}
	}
</script>

<div class="flex items-center gap-2">
	<div class="relative flex-1">
		<input
			{id}
			class="field pr-14"
			type="number"
			{min}
			{step}
			value={text}
			placeholder={unlimited ? unlimitedLabel : '0'}
			disabled={disabled || unlimited}
			oninput={(e) => onInput(e.currentTarget.value)}
		/>
		<span
			class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
			style="color: var(--text-faint)"
		>
			{unit}
		</span>
	</div>
	<label
		class="flex shrink-0 cursor-pointer items-center gap-1.5 text-xs whitespace-nowrap"
		style="color: var(--text-muted)"
	>
		<input
			type="checkbox"
			class="accent-[var(--primary)]"
			checked={unlimited}
			{disabled}
			onchange={(e) => setUnlimited(e.currentTarget.checked)}
		/>
		{unlimitedLabel}
	</label>
</div>
