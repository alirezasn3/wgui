import QRCode from 'qrcode';

const MONO = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace';

/**
 * WireGuard names a tunnel after the file it was imported from, and the name has
 * to fit a network interface: at most 15 characters, from a restricted set. The
 * clients reject anything else outright, so the file has to be named within the
 * rule even when the peer's name in the panel is longer.
 */
export const MAX_TUNNEL_NAME = 15;

const ALLOWED = /[^a-zA-Z0-9_=+.-]/g;

/** Length of the suffix that keeps shortened names apart, including its dash. */
const SUFFIX = 4;

/**
 * A short, stable digest of the full name.
 *
 * Truncating alone would give "Johnson-Family-Mom" and "Johnson-Family-Dad" the
 * same filename, and importing both onto one device would clash. The digest is
 * taken over the whole original name, so any difference anywhere survives.
 */
function shortHash(value: string): string {
	// FNV-1a, which is tiny and good enough to tell names apart.
	let h = 0x811c9dc5;
	for (let i = 0; i < value.length; i++) {
		h ^= value.charCodeAt(i);
		h = Math.imul(h, 0x01000193) >>> 0;
	}
	return h.toString(36).padStart(3, '0').slice(-3);
}

/** Turns a peer name into a filename WireGuard will accept, without the suffix. */
export function tunnelName(name: string): string {
	const trimmed = name.trim();
	const cleaned = trimmed.replace(ALLOWED, '-').replace(/-+/g, '-').replace(/^-|-$/g, '');

	if (!cleaned) return 'wgui';
	if (cleaned.length <= MAX_TUNNEL_NAME) return cleaned;

	const head = cleaned.slice(0, MAX_TUNNEL_NAME - SUFFIX).replace(/-$/, '');
	return `${head}-${shortHash(trimmed)}`;
}

/** The .conf filename for a peer. */
export function configFileName(name: string): string {
	return `${tunnelName(name)}.conf`;
}

/** Whether the peer's name had to be shortened to fit. */
export function nameWasShortened(name: string): boolean {
	return tunnelName(name) !== name.trim();
}

export interface QrOptions {
	/** The configuration the code encodes. */
	data: string;
	/** Side of the finished square image. The code is sized to fit inside it. */
	size: number;
	/** Colour of the dark modules and the labels. */
	color: string;
	/** Printed above the code, normally the peer's name. */
	topText?: string;
	/** Printed below the code, normally the peer's tunnel address. */
	bottomText?: string;
	/** An extra line under that, for whatever the operator wants to add. */
	caption?: string;
	/** Pixel density, so the image is crisp on a retina screen and when saved. */
	scale?: number;
}

/**
 * Draws the code with its labels onto canvas.
 *
 * The result is always square, whatever the labels, so it sits well anywhere it
 * is shared. The code is sized to whatever the labels leave behind, and the
 * whole thing is drawn on white regardless of the panel's theme, because a code
 * on a dark background will not scan.
 */
export async function drawLabelledQR(canvas: HTMLCanvasElement, o: QrOptions): Promise<void> {
	const scale = o.scale ?? Math.min(window.devicePixelRatio || 1, 3);
	const side = o.size;

	// Everything is a fraction of the side, so one square scales cleanly from
	// the panel's preview to the image that gets shared.
	const pad = Math.round(side * 0.04);
	const font = Math.max(7, Math.round(side * 0.035));
	const captionFont = Math.max(6, Math.round(side * 0.03));
	const line = Math.round(font * 1.5);
	const captionLine = Math.round(captionFont * 1.5);

	const top = o.topText ? line : 0;
	const bottom = (o.bottomText ? line : 0) + (o.caption ? captionLine : 0);
	const codeSize = side - pad * 2 - top - bottom;

	canvas.width = Math.round(side * scale);
	canvas.height = Math.round(side * scale);
	canvas.style.width = `${side}px`;
	canvas.style.height = `${side}px`;

	const ctx = canvas.getContext('2d');
	if (!ctx) throw new Error('this browser cannot draw the QR code');
	ctx.setTransform(scale, 0, 0, scale, 0, 0);

	ctx.fillStyle = '#ffffff';
	ctx.fillRect(0, 0, side, side);

	// The code is rendered on its own canvas first, so the label layout cannot
	// disturb the module grid.
	//
	// It is rasterised at the backing store's resolution rather than the
	// layout's. Drawing a code generated at CSS size into a scaled context
	// resamples every module, and the soft grey edge that leaves is the whole
	// difference between a preview that looks blurry and the exported image.
	const code = document.createElement('canvas');
	await QRCode.toCanvas(code, o.data, {
		width: Math.round(codeSize * scale),
		margin: 0,
		color: { dark: o.color, light: '#ffffff' },
		errorCorrectionLevel: 'M'
	});

	// The library rounds its own width down to a whole number of modules, so
	// the drawn size comes from what it actually produced. Source and
	// destination then line up one device pixel to one, and with resampling off
	// the module edges stay exactly as sharp as they were generated.
	const drawn = code.width / scale;
	const codeX = Math.round((side - drawn) / 2);
	const codeY = pad + top + Math.round((codeSize - drawn) / 2);
	ctx.imageSmoothingEnabled = false;
	ctx.drawImage(code, codeX, codeY, drawn, drawn);
	ctx.imageSmoothingEnabled = true;

	ctx.fillStyle = o.color;
	ctx.textAlign = 'center';
	ctx.textBaseline = 'middle';
	const centre = side / 2;

	if (o.topText) {
		ctx.font = `600 ${font}px ${MONO}`;
		ctx.fillText(o.topText, centre, pad + top / 2, side - pad * 2);
	}

	let y = pad + top + codeSize;
	if (o.bottomText) {
		ctx.font = `600 ${font}px ${MONO}`;
		ctx.fillText(o.bottomText, centre, y + line / 2, side - pad * 2);
		y += line;
	}
	if (o.caption) {
		ctx.font = `${captionFont}px ${MONO}`;
		ctx.fillText(o.caption, centre, y + captionLine / 2, side - pad * 2);
	}
}

/** Renders the labelled code and hands back a PNG. */
export async function qrPng(o: QrOptions): Promise<Blob> {
	const canvas = document.createElement('canvas');
	await drawLabelledQR(canvas, o);

	return new Promise((resolve, reject) => {
		canvas.toBlob((blob) => {
			if (blob) resolve(blob);
			else reject(new Error('the QR image could not be encoded'));
		}, 'image/png');
	});
}

/** Saves a blob under a given name. */
export function download(blob: Blob, filename: string): void {
	const url = URL.createObjectURL(blob);
	const link = document.createElement('a');
	link.href = url;
	link.download = filename;
	document.body.appendChild(link);
	link.click();
	link.remove();
	// Revoked on the next tick, so the download has started before the URL goes.
	setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** Whether this browser can share these files, which desktop ones often cannot. */
export function canShareFiles(files: File[]): boolean {
	return typeof navigator !== 'undefined' && !!navigator.canShare?.({ files });
}

/** Shares files through the operating system, returning false if declined. */
export async function shareFiles(files: File[], title: string, text?: string): Promise<boolean> {
	try {
		await navigator.share({ files, title, text });
		return true;
	} catch (e) {
		// Cancelling the sheet is not a failure worth reporting.
		if (e instanceof DOMException && e.name === 'AbortError') return false;
		throw e;
	}
}
