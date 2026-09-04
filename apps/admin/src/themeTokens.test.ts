import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('yellow and black theme tokens', () => {
  const css = readFileSync(resolve(process.cwd(), 'tokens.css'), 'utf8').toLowerCase()

  it('keeps the approved brand and warm neutral palette', () => {
    expect(css).toContain('--color-brand: #ffd633')
    expect(css).toContain('--color-brand-strong: #171714')
    expect(css).toContain('--color-brand-soft: #fff1a6')
    expect(css).toContain('--color-canvas: #f7f5ee')
    expect(css).toContain('--color-paper: #fffdf8')
    expect(css).toContain('--color-primary-ink: #ffd633')
  })

  it('does not restore the former green brand palette', () => {
    for (const formerBrandColor of ['#12b76a', '#039855', '#027a48', '#ecfdf3', '#a6f4c5']) {
      expect(css).not.toContain(formerBrandColor)
    }
  })
})
