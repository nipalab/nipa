import type { IconProps } from '@primer/octicons-react'
import {
  BookIcon,
  DatabaseIcon,
  FileBinaryIcon,
  FileCodeIcon,
  FileDirectoryFillIcon,
  FileIcon,
  FileMediaIcon,
  FileZipIcon,
  GlobeIcon,
  PackageIcon,
  TableIcon,
  TerminalIcon,
  VideoIcon,
} from '@primer/octicons-react'
import type { ComponentType } from 'react'

type IconComponent = ComponentType<IconProps>

const CODE = new Set([
  'c', 'cc', 'clj', 'cpp', 'cs', 'cxx', 'dart', 'erl', 'ex', 'exs', 'go', 'groovy', 'h', 'hh', 'hpp',
  'hs', 'java', 'js', 'jsx', 'kt', 'kts', 'lua', 'm', 'mm', 'php', 'pl', 'pm', 'proto', 'py', 'r',
  'rb', 'rs', 'scala', 'sol', 'swift', 'ts', 'tsx', 'vala', 'zig',
])

const MARKUP = new Set(['htm', 'html', 'vue', 'svelte'])

const DATA = new Set(['ini', 'json', 'jsonc', 'sql', 'toml', 'yaml', 'yml'])

const IMAGE = new Set(['avif', 'bmp', 'gif', 'ico', 'jpeg', 'jpg', 'png', 'psd', 'svg', 'tif', 'tiff', 'webp'])

const VIDEO = new Set(['avi', 'flv', 'm4v', 'mkv', 'mov', 'mp4', 'webm', 'wmv'])

const AUDIO = new Set(['aac', 'flac', 'm4a', 'mp3', 'ogg', 'wav', 'wma'])

const ARCHIVE = new Set(['7z', 'bz2', 'gz', 'lz4', 'rar', 'tar', 'tgz', 'xz', 'zip', 'zst'])

const BINARY = new Set([
  'a', 'bin', 'class', 'dat', 'dll', 'dylib', 'exe', 'lib', 'o', 'obj', 'pak', 'pdb', 'pyc', 'so', 'wasm',
])

const SHELL = new Set(['bash', 'bat', 'cmd', 'fish', 'ps1', 'sh', 'zsh'])

const TABLE = new Set(['csv', 'tsv', 'xls', 'xlsx'])

const PACKAGE = new Set(['lock', 'sum'])

export function fileIcon(name: string, type: 'tree' | 'file'): { Icon: IconComponent; color: string } {
  if (type === 'tree') {
    return { Icon: FileDirectoryFillIcon, color: 'var(--fgColor-accent)' }
  }
  const lower = name.toLowerCase()
  const dot = lower.lastIndexOf('.')
  const ext = dot >= 0 ? lower.slice(dot + 1) : ''
  if (lower.startsWith('readme')) {
    return { Icon: BookIcon, color: 'var(--fgColor-accent)' }
  }
  if (lower === 'dockerfile' || lower === 'makefile' || SHELL.has(ext)) {
    return { Icon: TerminalIcon, color: 'var(--fgColor-muted)' }
  }
  if (lower === 'package.json' || lower.endsWith('.lock') || PACKAGE.has(ext)) {
    return { Icon: PackageIcon, color: 'var(--fgColor-attention)' }
  }
  if (IMAGE.has(ext)) {
    return { Icon: FileMediaIcon, color: 'var(--fgColor-done)' }
  }
  if (VIDEO.has(ext)) {
    return { Icon: VideoIcon, color: 'var(--fgColor-done)' }
  }
  if (AUDIO.has(ext)) {
    return { Icon: FileMediaIcon, color: 'var(--fgColor-done)' }
  }
  if (ARCHIVE.has(ext)) {
    return { Icon: FileZipIcon, color: 'var(--fgColor-attention)' }
  }
  if (TABLE.has(ext)) {
    return { Icon: TableIcon, color: 'var(--fgColor-success)' }
  }
  if (DATA.has(ext)) {
    return { Icon: DatabaseIcon, color: 'var(--fgColor-success)' }
  }
  if (MARKUP.has(ext)) {
    return { Icon: GlobeIcon, color: 'var(--fgColor-attention)' }
  }
  if (CODE.has(ext)) {
    return { Icon: FileCodeIcon, color: 'var(--fgColor-muted)' }
  }
  if (BINARY.has(ext)) {
    return { Icon: FileBinaryIcon, color: 'var(--fgColor-danger)' }
  }
  return { Icon: FileIcon, color: 'var(--fgColor-muted)' }
}
