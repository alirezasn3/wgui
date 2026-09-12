<script lang="ts">
	/**
	 * An expiry expressed as a number of days from now, paired with a "Never"
	 * switch. The API stores 0 for "no expiry".
	 *
	 * A day count cannot represent a deadline that has already passed, which is
	 * where the old control went wrong: it clamped a past expiry to 0, and 0 is
	 * what the panel stores for "never", so opening an expired peer and saving
	 * anything at all quietly made it never expire. Two things fix that. The
	 * field only reports a value once the operator actually touches it, so an
	 * untouched dialog leaves the stored expiry exactly as it was; and the line
	 * underneath always states the expiry the peer has right now, so an expired
	 * one can never be mistaken for one that never expires.
	 */
	import { absolute, relative } from '$lib/format';

	const DAY = 86_400_000;

	interface Props {
		/** The expiry in force, unix ms; 0 means never. */
		value: number;
		id?: string;
		disabled?: boolean;
		/** Called only on a real edit, with the new number of days (0 = never). */
		onchange: (days: number) => void;
	}
	let { value, id, disabled = false, onchange }: Props = $props();

	let never = $state(false);
	let text = $state('');
	let remembered = $state(30);
	let touched = $state(false);

	// Adopt the prop only when it changes from outside; typing keeps its own
	// state so a half-entered number is not reformatted under the cursor.
	let lastValue = $state<number | null>(null);
	$effect(() => {
		if (value === lastValue) return;
		lastValue = value;
		touched = false;
		never = !value;
		// An expiry in the past has no positive day count, so the field starts
		// empty and the caption below carries the truth.
		const days = value ? Math.ceil((value - Date.now()) / DAY) : 0;
		if (days > 0) remembered = days;
		text = days > 0 ? String(days) : '';
	});

	function emit(days: number) {
		touched = true;
		onchange(days);
	}

	function onInput(raw: string) {
		text = raw;
		const parsed = Number(raw);
		emit(raw.trim() === '' || Number.isNaN(parsed) ? 0 : Math.max(Math.round(parsed), 0));
	}

	function setNever(on: boolean) {
		never = on;
		if (on) {
			text = '';
			emit(0);
		} else {
			text = String(remembered);
			emit(remembered);
		}
	}

	let current = $derived(
		!value
			? 'Never expires'
			: value <= Date.now()
				? `Expired ${relative(value)}`
				: `Expires ${relative(value)}, on ${absolute(value)}`
	);
	let pending = $derived(
		!touched
			? ''
			: never
				? 'will be changed to never expire'
				: `will be ${absolute(Date.now() + Number(text || 0) * DAY)}`
	);
</script>

<div class="flex items-center gap-2">
	<div class="relative flex-1">
		<input
			{id}
			class="field pr-14"
			type="number"
			min="0"
			step="1"
			value={text}
			placeholder={never ? 'Never' : '0'}
			disabled={disabled || never}
			oninput={(e) => onInput(e.currentTarget.value)}
		/>
		<span
			class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs"
			style="color: var(--text-faint)"
		>
			days
		</span>
	</div>
	<label
		class="flex shrink-0 cursor-pointer items-center gap-1.5 text-xs whitespace-nowrap"
		style="color: var(--text-muted)"
	>
		<input
			type="checkbox"
			class="accent-[var(--primary)]"
			checked={never}
			{disabled}
			onchange={(e) => setNever(e.currentTarget.checked)}
		/>
		Never
	</label>
</div>

<p
	class="mt-1 text-[11px]"
	style="color: {value && value <= Date.now() && !touched ? 'var(--warning)' : 'var(--text-faint)'}"
>
	{current}{pending ? ` — ${pending}` : ''}
</p>
