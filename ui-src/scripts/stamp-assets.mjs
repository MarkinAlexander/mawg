import { createHash } from 'node:crypto'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const appDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../internal/web/ui/app')

// штампуем только существующие файлы: styles.css появится, когда у
// компонентов появится собственный CSS
const stamp = (html, file) => {
  const full = path.join(appDir, file)
  if (!existsSync(full)) return { html, v: null }
  const v = createHash('sha256').update(readFileSync(full)).digest('hex').slice(0, 8)
  const re = file === 'app.js'
    ? new RegExp(`(<script[^>]*src="[^"]*${file})("[^>]*>)`)
    : new RegExp(`(<link[^>]*href="[^"]*${file})("[^>]*>)`)
  return { html: html.replace(re, `$1?v=${v}$2`), v }
}

let html = readFileSync(path.join(appDir, 'index.html'), 'utf8').replace(/\r\n/g, '\n')
for (const file of ['app.js', 'styles.css', 'panel.css']) {
  const r = stamp(html, file)
  html = r.html
  if (r.v) console.log(`${file}?v=${r.v}`)
}
writeFileSync(path.join(appDir, 'index.html'), html)
