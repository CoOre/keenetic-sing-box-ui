// CodeMirror JSON editor. Loaded with a dynamic import from JsonEditor so the
// editor (~half of the bundle) only downloads when an editor is on screen.
import { basicSetup } from "codemirror";
import { EditorView, keymap, placeholder as cmPlaceholder } from "@codemirror/view";
import { EditorState } from "@codemirror/state";
import { indentWithTab } from "@codemirror/commands";
import { json, jsonParseLinter } from "@codemirror/lang-json";
import { linter, lintGutter, setDiagnostics, type Diagnostic } from "@codemirror/lint";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags as t } from "@lezer/highlight";

export interface EditorProblem {
  line?: number; // 1-based; without a position the problem is located by a tag it quotes
  col?: number;
  message: string;
  severity?: "error" | "warning";
}

export interface JsonEditorHandle {
  setValue(v: string): void;
  setProblems(problems: EditorProblem[]): void;
  destroy(): void;
}

const highlight = HighlightStyle.define([
  { tag: t.propertyName, color: "#aecbff" },
  { tag: t.string, color: "#7ee3c2" },
  { tag: t.number, color: "#f6cd86" },
  { tag: [t.bool, t.null], color: "#d7a8ff" },
  { tag: [t.brace, t.squareBracket, t.separator, t.punctuation], color: "#9aa6b6" },
  { tag: t.invalid, color: "#f6a3a2" },
]);

const theme = EditorView.theme(
  {
    "&": { backgroundColor: "#070a0e", color: "#e8edf4", fontSize: "12.5px", maxHeight: "70vh" },
    "&.cm-focused": { outline: "none" },
    ".cm-scroller": { fontFamily: "var(--mono)", lineHeight: "1.7" },
    ".cm-content": { padding: "12px 0", caretColor: "#6aa0ff" },
    ".cm-cursor, .cm-dropCursor": { borderLeftColor: "#6aa0ff" },
    ".cm-gutters": { backgroundColor: "#070a0e", color: "#687385", border: "none" },
    ".cm-activeLine": { backgroundColor: "rgba(77,139,255,0.06)" },
    ".cm-activeLineGutter": { backgroundColor: "rgba(77,139,255,0.10)", color: "#9aa6b6" },
    "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": {
      backgroundColor: "rgba(77,139,255,0.28) !important",
    },
    ".cm-matchingBracket": { backgroundColor: "rgba(54,207,155,0.18)", outline: "none" },
    ".cm-foldPlaceholder": { backgroundColor: "#1c2531", border: "none", color: "#9aa6b6" },
    ".cm-tooltip": { backgroundColor: "#121822", border: "1px solid #273140", color: "#e8edf4" },
    ".cm-tooltip-autocomplete ul li[aria-selected]": { backgroundColor: "rgba(77,139,255,0.25)" },
    ".cm-panels": { backgroundColor: "#0d1118", color: "#e8edf4" },
    ".cm-searchMatch": { backgroundColor: "rgba(243,179,76,0.25)" },
    ".cm-placeholder": { color: "#687385" },
    ".cm-diagnostic-error": { borderLeftColor: "#f0706f" },
    ".cm-diagnostic-warning": { borderLeftColor: "#f3b34c" },
  },
  { dark: true },
);

export function createJsonEditor(
  parent: HTMLElement,
  opts: { value: string; placeholder?: string; minHeight: string; onChange: (v: string) => void },
): JsonEditorHandle {
  const sizing = EditorView.theme({ "&": { minHeight: opts.minHeight }, ".cm-scroller": { minHeight: opts.minHeight } });
  let pushing = false; // a setValue from the parent must not echo back as a change

  const view = new EditorView({
    parent,
    state: EditorState.create({
      doc: opts.value,
      extensions: [
        basicSetup,
        keymap.of([indentWithTab]),
        json(),
        linter(jsonParseLinter(), { delay: 300 }),
        lintGutter(),
        syntaxHighlighting(highlight),
        theme,
        sizing,
        EditorState.tabSize.of(2),
        opts.placeholder ? cmPlaceholder(opts.placeholder) : [],
        EditorView.updateListener.of((u) => {
          if (u.docChanged && !pushing) opts.onChange(u.state.doc.toString());
        }),
      ],
    }),
  });

  return {
    setValue(v) {
      if (v === view.state.doc.toString()) return;
      pushing = true;
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } });
      pushing = false;
    },
    // Server-side problems: positioned ones go to their line:col, the rest to
    // the first occurrence of a tag they quote; unlocatable ones are skipped
    // (the card lists every problem anyway).
    setProblems(problems) {
      const doc = view.state.doc;
      const text = doc.toString();
      const diags: Diagnostic[] = [];
      for (const p of problems) {
        let from = -1;
        let to = -1;
        if (p.line && p.line >= 1 && p.line <= doc.lines) {
          const line = doc.line(p.line);
          from = Math.min(line.from + Math.max((p.col ?? 1) - 1, 0), line.to);
          to = Math.min(from + 1, line.to);
          if (to === from) from = Math.max(line.from, from - 1);
        } else {
          for (const m of p.message.matchAll(/"([^"]+)"/g)) {
            const i = text.indexOf(`"${m[1]}"`);
            if (i >= 0) { from = i; to = i + m[1].length + 2; break; }
          }
        }
        if (from >= 0) diags.push({ from, to: Math.max(to, from), severity: p.severity ?? "error", message: p.message });
      }
      view.dispatch(setDiagnostics(view.state, diags));
    },
    destroy() { view.destroy(); },
  };
}
