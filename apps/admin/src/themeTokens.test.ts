import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const css = readFileSync(resolve(process.cwd(), 'tokens.css'), 'utf8').toLowerCase()
const token = (name: string) => {
  const value = css.match(new RegExp(`${name}: (#[0-9a-f]{6})`))?.[1]
  if (!value) throw new Error(`Missing color token: ${name}`)
  return value
}
function luminance(hex: string) {
  const rgb = hex.slice(1).match(/../g)!.map((v) => parseInt(v, 16) / 255)
    .map((v) => v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4)
  return rgb[0] * .2126 + rgb[1] * .7152 + rgb[2] * .0722
}
function contrast(a: string, b: string) {
  const x = luminance(token(a)), y = luminance(token(b))
  return (Math.max(x, y) + .05) / (Math.min(x, y) + .05)
}

describe('blue and white theme accessibility', () => {
  it('uses blue branding but keeps the live status dot semantic green', () => {
    expect(token('--color-brand-logo')).toBe('#1976b9')
    expect(token('--color-success')).toBe('#168f47')
    const styles = readFileSync(resolve(process.cwd(), 'src/tailadmin.css'), 'utf8')
    const dot = styles.match(/\.tailadmin-shell \.pulse-dot\s*\{([^}]+)\}/)?.[1]
    expect(dot).toContain('background: var(--color-success)')
    expect(dot).not.toContain('var(--color-brand-logo)')
  })

  it('keeps navigation light and separates selected surfaces from action fills', () => {
    expect(token('--sidebar')).toBe('#ffffff')
    expect(token('--color-primary')).toBe('#1976b9')
    expect(token('--color-selected')).toBe('#e7f2fc')
    expect(token('--color-selected')).not.toBe(token('--color-primary'))
  })

  it('keeps text, links, selected items and pressed buttons readable', () => {
    for (const surface of ['--color-paper', '--color-canvas', '--color-paper-subtle', '--color-selected']) {
      for (const text of ['--color-ink', '--color-ink-secondary', '--color-link']) {
        expect(contrast(text, surface)).toBeGreaterThanOrEqual(4.5)
      }
    }
    for (const fill of ['--color-primary', '--color-primary-hover']) {
      expect(contrast('--color-primary-ink', fill)).toBeGreaterThanOrEqual(4.5)
    }
    expect(contrast('--color-rule-strong', '--color-paper-subtle')).toBeGreaterThanOrEqual(3)
  })

  it('does not leave the previous yellow palette in the style cascade', () => {
    const styles = ['tokens.css', 'src/styles.css', 'src/tailadmin.css', 'src/user-access.css']
      .map((file) => readFileSync(resolve(process.cwd(), file), 'utf8').toLowerCase()).join('\n')
    expect(styles).not.toMatch(/#(?:ffd633|e6b900|fff1a6|ffe36b|171714|fff8de)|255\s+214\s+51/)
  })
})
