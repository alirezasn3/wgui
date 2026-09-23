/**
 * Just enough Markdown for release notes: headings, lists, paragraphs, code,
 * bold, emphasis and links.
 *
 * It produces tokens rather than HTML, and the component that draws them never
 * uses {@html}, so nothing in a changelog can put markup into the panel. That
 * matters more than it looks: the panel runs with the authority of whoever is
 * viewing it, and an admin reads the notes.
 */

export type Inline =
	| { kind: 'text'; text: string }
	| { kind: 'code'; text: string }
	| { kind: 'strong'; text: string }
	| { kind: 'em'; text: string }
	| { kind: 'link'; text: string; href: string };

export type Block =
	| { kind: 'heading'; level: number; inline: Inline[] }
	| { kind: 'list'; ordered: boolean; items: Inline[][] }
	| { kind: 'paragraph'; inline: Inline[] }
	| { kind: 'code'; text: string };

const heading = /^(#{1,6})\s+(.*?)\s*#*\s*$/;
const bullet = /^\s*[-*+]\s+(.*)$/;
const numbered = /^\s*\d+[.)]\s+(.*)$/;

export function parseMarkdown(source: string): Block[] {
	const lines = source
		.replace(/\r\n/g, '\n')
		.replace(/<!--[\s\S]*?-->/g, '')
		.split('\n');
	const blocks: Block[] = [];
	let paragraph: string[] = [];
	let list: { ordered: boolean; items: string[] } | null = null;

	const flush = () => {
		if (paragraph.length) {
			blocks.push({ kind: 'paragraph', inline: parseInline(paragraph.join(' ')) });
			paragraph = [];
		}
		if (list) {
			blocks.push({ kind: 'list', ordered: list.ordered, items: list.items.map(parseInline) });
			list = null;
		}
	};

	for (let i = 0; i < lines.length; i++) {
		const line = lines[i];

		if (line.trimStart().startsWith('```')) {
			flush();
			const code: string[] = [];
			for (i++; i < lines.length && !lines[i].trimStart().startsWith('```'); i++) {
				code.push(lines[i]);
			}
			blocks.push({ kind: 'code', text: code.join('\n') });
			continue;
		}
		if (!line.trim()) {
			flush();
			continue;
		}

		const h = heading.exec(line);
		if (h) {
			flush();
			blocks.push({ kind: 'heading', level: h[1].length, inline: parseInline(h[2]) });
			continue;
		}

		const b = bullet.exec(line) ?? numbered.exec(line);
		if (b) {
			const ordered = !bullet.test(line);
			if (paragraph.length || (list && list.ordered !== ordered)) flush();
			list ??= { ordered, items: [] };
			list.items.push(b[1]);
			continue;
		}

		// An indented line under a list item carries that item on.
		if (list && /^\s{2,}/.test(line)) {
			list.items[list.items.length - 1] += ' ' + line.trim();
			continue;
		}
		if (list) flush();
		paragraph.push(line.trim());
	}
	flush();
	return blocks;
}

const inlinePattern =
	/`([^`]+)`|\*\*([^*]+)\*\*|__([^_]+)__|\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)|(https?:\/\/[^\s)<>]+)|\*([^*\s][^*]*)\*|_([^_\s][^_]*)_/g;

export function parseInline(text: string): Inline[] {
	const out: Inline[] = [];
	let last = 0;
	for (const m of text.matchAll(inlinePattern)) {
		if (m.index > last) out.push({ kind: 'text', text: text.slice(last, m.index) });
		if (m[1] !== undefined) out.push({ kind: 'code', text: m[1] });
		else if (m[2] !== undefined || m[3] !== undefined)
			out.push({ kind: 'strong', text: m[2] ?? m[3] });
		else if (m[4] !== undefined) out.push({ kind: 'link', text: m[4], href: m[5] });
		else if (m[6] !== undefined) out.push({ kind: 'link', text: m[6], href: m[6] });
		else out.push({ kind: 'em', text: m[7] ?? m[8] });
		last = m.index + m[0].length;
	}
	if (last < text.length) out.push({ kind: 'text', text: text.slice(last) });
	return out;
}
