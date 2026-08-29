export function fileRootLabel(path: string, index: number): string {
  if (path === '/etc/nginx') return 'Nginx Config'
  if (path === '/etc/php') return 'PHP Config'
  if (path === '/var/log') return 'System Logs'
  if (path === '/home') return 'Home'
  if (index === 0) return 'Web Vhosts'
  return path.split('/').filter(Boolean).pop() ?? path
}

export function activeFileRoot(path: string, roots: string[]): string {
  return [...roots]
    .sort((a, b) => b.length - a.length)
    .find((root) => path === root || path.startsWith(`${root}/`)) ?? path
}
