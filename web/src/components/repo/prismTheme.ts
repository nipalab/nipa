import type { PrismTheme } from 'prism-react-renderer'

export const githubDarkTheme: PrismTheme = {
  plain: {
    color: '#c9d1d9',
    backgroundColor: 'transparent',
  },
  styles: [
    { types: ['comment', 'prolog', 'doctype', 'cdata'], style: { color: '#8b949e' } },
    { types: ['namespace'], style: { opacity: 0.7 } },
    { types: ['string', 'attr-value', 'char', 'regex'], style: { color: '#a5d6ff' } },
    { types: ['punctuation', 'operator'], style: { color: '#c9d1d9' } },
    { types: ['number', 'boolean', 'constant', 'symbol', 'variable', 'deleted'], style: { color: '#79c0ff' } },
    { types: ['function', 'class-name', 'method'], style: { color: '#d2a8ff' } },
    { types: ['keyword', 'storage', 'atrule', 'selector'], style: { color: '#ff7b72' } },
    { types: ['tag', 'inserted'], style: { color: '#7ee787' } },
    { types: ['attr-name', 'property', 'entity', 'url'], style: { color: '#79c0ff' } },
    { types: ['builtin', 'title', 'important', 'bold'], style: { color: '#ffa657', fontWeight: 'bold' } },
    { types: ['italic'], style: { fontStyle: 'italic' } },
  ],
}
