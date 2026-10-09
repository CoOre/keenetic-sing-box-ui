// Minimal Markdown → HTML for release notes (ours from scripts/changelog.sh
// and sing-box's docs/changelog.md): headings, nested lists, paragraphs,
// pipe tables, fenced code, **bold**, `code`, [links](http…). All text is HTML-escaped
// first and only http(s) links survive, so the output is safe for {@html}.

const esc = (s: string) =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

function inline(s: string): string {
  const codes: string[] = [];
  const out = esc(s)
    .replace(/`([^`]+)`/g, (_, c) => `\u0000${codes.push(c) - 1}\u0000`)
    .replace(/(^|\s):[a-z0-9_+-]+:(?=\s|$)/g, "$1") // emoji shortcodes (:memo:)
    .replace(/\*\*([^*]+)\*\*/g, "<b>$1</b>")
    .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, text, url) =>
      /^https?:\/\//.test(url)
        ? `<a href="${url}" target="_blank" rel="noopener noreferrer">${text}</a>`
        : text,
    );
  return out.replace(/\u0000(\d+)\u0000/g, (_, i) => `<code>${codes[+i]}</code>`).trim();
}

const isRow = (l: string) => /^\s*\|.*\|\s*$/.test(l);
const isSep = (l: string) => /^\s*\|?(\s*:?-+:?\s*\|)+\s*:?-*:?\s*\|?\s*$/.test(l);
const cells = (l: string) =>
  l.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((c) => inline(c));

function table(rows: string[]): string {
  const [head, , ...body] = rows;
  const tr = (cs: string[], tag: string) => `<tr>${cs.map((c) => `<${tag}>${c}</${tag}>`).join("")}</tr>`;
  return (
    `<div class="md-table"><table><thead>${tr(cells(head), "th")}</thead>` +
    `<tbody>${body.map((r) => tr(cells(r), "td")).join("")}</tbody></table></div>`
  );
}

export function renderMarkdown(md: string): string {
  const lines = md.replace(/\r\n/g, "\n").split("\n");
  const html: string[] = [];
  const lists: number[] = []; // indent of each open <ul>
  let para: string[] = [];
  let liOpen = false;
  let afterBlank = false; // previous line was blank

  const flushPara = () => {
    if (para.length) html.push(`<p>${inline(para.join(" "))}</p>`);
    para = [];
  };
  const closeLists = (toIndent = -1) => {
    while (lists.length && lists[lists.length - 1] > toIndent) {
      html.push("</li></ul>");
      lists.pop();
    }
    liOpen = lists.length > 0;
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const blankBefore = afterBlank;
    afterBlank = false;

    if (isRow(line) && i + 1 < lines.length && isSep(lines[i + 1])) {
      flushPara();
      closeLists();
      const rows = [line, lines[++i]];
      while (i + 1 < lines.length && isRow(lines[i + 1])) rows.push(lines[++i]);
      html.push(table(rows));
      continue;
    }

    if (/^\s*```/.test(line)) {
      flushPara();
      closeLists();
      const code: string[] = [];
      while (++i < lines.length && !/^\s*```/.test(lines[i])) code.push(lines[i]);
      html.push(`<pre><code>${esc(code.join("\n"))}</code></pre>`);
      continue;
    }

    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      flushPara();
      closeLists();
      const text = inline(h[2]);
      if (text) html.push(`<h${Math.min(h[1].length + 3, 6)}>${text}</h${Math.min(h[1].length + 3, 6)}>`);
      continue;
    }

    const li = /^(\s*)[-*+]\s+(.*)$/.exec(line);
    if (li) {
      flushPara();
      const indent = li[1].replace(/\t/g, "    ").length;
      if (!lists.length || indent > lists[lists.length - 1]) {
        html.push("<ul>");
        lists.push(indent);
      } else {
        closeLists(indent);
        if (!lists.length) {
          html.push("<ul>");
          lists.push(indent);
        } else html.push("</li>");
      }
      html.push(`<li>${inline(li[2])}`);
      liOpen = true;
      continue;
    }

    if (!line.trim()) {
      flushPara();
      afterBlank = true;
      continue;
    }

    // Indented continuation of a list item (lazy wrap, no blank line between);
    // anything else — including text after a blank line — ends the lists.
    if (liOpen && !blankBefore && /^\s+\S/.test(line)) {
      html.push(" " + inline(line));
      continue;
    }
    closeLists();
    para.push(line.trim().replace(/^!!!\s+\w+\s*/, ""));
  }
  flushPara();
  closeLists();
  return html.join("");
}
