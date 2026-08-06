// 统一的 Markdown 渲染：marked + KaTeX 数学公式
// - 块级公式 $$...$$ 与行内公式 $...$ 用 KaTeX 渲染（解析失败时原样显示）
// - 代码块/行内代码内的 $ 不受公式解析影响（先保护后还原）
// - 保留防 XSS（移除 <script>）

import { marked } from 'marked';
import katex from 'katex';
import 'katex/dist/katex.min.css';

marked.setOptions({ breaks: true, gfm: true });

const renderFormula = (expr, displayMode) => {
  try {
    return katex.renderToString(expr, {
      displayMode,
      throwOnError: false,
    });
  } catch (e) {
    return null;
  }
};

export function renderMarkdown(content) {
  if (!content) return '';
  const src = String(content);

  // 1. 保护代码块与行内代码（公式解析不进入代码内容）
  const codes = [];
  const text = src.replace(/(```[\s\S]*?```|`[^`\n]*`)/g, (m) => {
    codes.push(m);
    return `\u0000C${codes.length - 1}\u0000`;
  });

  // 2. 块级公式 $$...$$（可跨行，先于行内公式处理）
  let out = text.replace(/\$\$([\s\S]+?)\$\$/g, (m, expr) => {
    const html = renderFormula(expr.trim(), true);
    return html ? `<div class="math-block">${html}</div>` : m;
  });

  // 3. 行内公式 $...$（以字母或反斜杠开头，避免误伤 $5 等价格写法）
  out = out.replace(/(^|[^\\])\$([a-zA-Z\\][^$]*?)\$/g, (m, pre, expr) => {
    const html = renderFormula(expr.trim(), false);
    return html ? `${pre}${html}` : m;
  });

  // 4. 还原代码块
  out = out.replace(/\u0000C(\d+)\u0000/g, (m, i) => codes[Number(i)]);

  // 5. marked 渲染
  let html = marked.parse(out);
  html = html.replace(/<script[\s\S]*?<\/script>/gi, '');
  return html;
}
