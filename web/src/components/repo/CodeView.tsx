import { Highlight } from 'prism-react-renderer'
import { languageFor } from './language'
import { githubDarkTheme } from './prismTheme'

export function CodeView({ code, path }: { code: string; path: string }) {
  const language = languageFor(path)
  const trimmed = code.endsWith('\n') ? code.slice(0, -1) : code
  return (
    <Highlight theme={githubDarkTheme} code={trimmed} language={language}>
      {({ tokens, getLineProps, getTokenProps }) => (
        <pre
          style={{
            margin: 0,
            padding: '16px 0',
            overflowX: 'auto',
            fontSize: 12.5,
            lineHeight: '20px',
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
            background: 'transparent',
          }}
        >
          {tokens.map((line, index) => {
            const lineProps = getLineProps({ line })
            return (
              <div
                key={index}
                {...lineProps}
                className={`nipa-code-line ${lineProps.className ?? ''}`}
                style={{ display: 'flex', ...lineProps.style }}
              >
                <span
                  aria-hidden
                  style={{
                    width: 56,
                    flexShrink: 0,
                    paddingRight: 16,
                    textAlign: 'right',
                    color: 'var(--fgColor-muted)',
                    userSelect: 'none',
                  }}
                >
                  {index + 1}
                </span>
                <span style={{ flex: 1, paddingRight: 16 }}>
                  {line.map((token, key) => (
                    <span key={key} {...getTokenProps({ token })} />
                  ))}
                </span>
              </div>
            )
          })}
        </pre>
      )}
    </Highlight>
  )
}
