// 统一的 Markdown 渲染：marked + KaTeX 数学公式
// - 块级公式 $$...$$ / \[...\] 与行内公式 $...$ / \(...\) 用 KaTeX 渲染（解析失败时原样显示）
// - 代码块/行内代码内的 $ 不受公式解析影响（先保护后还原）
// - 保留防 XSS（移除 <script>）

import { marked } from 'marked';
import katex from 'katex';
import DOMPurify from 'dompurify';
import 'katex/dist/katex.min.css';

marked.setOptions({ breaks: true, gfm: true });

// KaTeX 渲染依赖内联 style 定位（上标/分数/间距），必须保留；
// 其余节点一律移除 style 属性，防止 UI 劫持类样式注入
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.hasAttribute && node.hasAttribute('style') && node.closest && !node.closest('.katex')) {
    node.removeAttribute('style');
  }
});

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

  // 2. 块级公式 $$...$$ 或 \[...\]（可跨行，先于行内公式处理）
  let out = text
    .replace(/\$\$([\s\S]+?)\$\$/g, (m, expr) => {
      const html = renderFormula(expr.trim(), true);
      return html ? `<div class="math-block">${html}</div>` : m;
    })
    .replace(/\\\[([\s\S]+?)\\\]/g, (m, expr) => {
      const html = renderFormula(expr.trim(), true);
      return html ? `<div class="math-block">${html}</div>` : m;
    });

  // 3. 行内公式 $...$（首字符不限字母：支持 $3uv+p=0$、$(u+v)^3$ 等；
  //    排除 $$ 块（块级已处理）、\$ 转义、以及 $5 / $5.99 等价格写法）或 \(...\)
  out = out
    .replace(/(^|[^\\$])\$(?!\$)([^\s$][^$\n]*?)\$(?![\d.])/g, (m, pre, expr) => {
      const trimmed = expr.trim();
      if (/^\d+(?:[.,]\d+)*$/.test(trimmed)) return m;
      const html = renderFormula(trimmed, false);
      return html ? `${pre}${html}` : m;
    })
    .replace(/\\\(([\s\S]+?)\\\)/g, (m, expr) => {
      const html = renderFormula(expr.trim(), false);
      return html || m;
    });

  // 4. 还原代码块
  out = out.replace(/\u0000C(\d+)\u0000/g, (m, i) => codes[Number(i)]);

  // 5. marked 渲染 + 安全过滤（DOMPurify：只保留安全标签，防 XSS）
  let html = marked.parse(out);
  return DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['style', 'form', 'input', 'button', 'iframe', 'object', 'embed', 'link', 'meta', 'base'],
    // 事件属性一律移除；style 交给上面的 hook 处理（仅 KaTeX 保留）
    FORBID_ATTR: ['onerror', 'onload', 'onclick', 'onmouseover', 'onfocus', 'onblur', 'onchange', 'onsubmit', 'onkeydown', 'onkeyup', 'onkeypress'],
  });
}
