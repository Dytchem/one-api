// 统一的 Markdown 渲染：markdown-it + markdown-it-texmath（成熟生态）
// 替代原自研正则公式解析（marked + 手写 $ 边界正则）：
// - texmath 原生支持 $...$ / $$...$$ / \(...\) / \[...\]，含代码块/行内代码内的
//   $ 天然不受公式解析影响（markdown-it 语法树隔离，无需手工保护还原）
// - 公式内中文正常渲染（原实现遇到 CJK 直接跳过不渲染）
// - 解析失败（throwOnError: false + strict: false）原样回退为普通文本，不报错
import MarkdownIt from 'markdown-it';
import texmath from 'markdown-it-texmath';
import katex from 'katex';
import DOMPurify from 'dompurify';
import 'katex/dist/katex.min.css';

const md = new MarkdownIt({ breaks: true, html: true });

md.use(texmath, {
  engine: katex,
  delimiters: ['dollars', 'brackets'],
  katexOptions: { throwOnError: false, strict: false },
});

// KaTeX 渲染依赖内联 style 定位（上标/分数/间距），必须保留；
// 其余节点一律移除 style 属性，防止 UI 劫持类样式注入
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.hasAttribute && node.hasAttribute('style') && node.closest && !node.closest('.katex')) {
    node.removeAttribute('style');
  }
});

// KaTeX 输出依赖的标签/属性：根号等符号由内联 SVG path 绘制，MathML 供无障碍使用。
// DOMPurify 的 html profile 不含 svg/math 标签，必须显式放行，否则根号等符号被删
const katexTags = ['svg', 'path', 'g', 'line', 'rect', 'polyline', 'math', 'semantics', 'mrow', 'mi', 'mo', 'mn', 'msqrt', 'mfrac', 'msup', 'msub', 'mtext', 'mstyle', 'annotation'];
const katexAttrs = ['viewBox', 'preserveAspectRatio', 'fill', 'stroke', 'stroke-width', 'stroke-linecap', 'stroke-linejoin', 'xmlns', 'xlink:href', 'width', 'height', 'd'];

export function renderMarkdown(content) {
  if (!content) return '';
  const html = md.render(String(content));
  return DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    ADD_TAGS: katexTags,
    ADD_ATTR: katexAttrs,
    FORBID_TAGS: ['style', 'form', 'input', 'button', 'iframe', 'object', 'embed', 'link', 'meta', 'base'],
    // 事件属性一律移除；style 交给上面的 hook 处理（仅 KaTeX 保留）
    FORBID_ATTR: ['onerror', 'onload', 'onclick', 'onmouseover', 'onfocus', 'onblur', 'onchange', 'onsubmit', 'onkeydown', 'onkeyup', 'onkeypress'],
  });
}
