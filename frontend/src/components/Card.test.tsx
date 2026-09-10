// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { DocumentCard } from './Card'

// jsdom does not compute layout, so the CSS animation itself isn't tested;
// this pins the TR-14 contract: content stays mounted while collapsed (no
// state loss), the wrapper carries the open class, and aria-expanded flips.
let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

describe('DocumentCard', () => {
  it('keeps children mounted while collapsed and toggles via the header', () => {
    act(() =>
      root.render(
        <DocumentCard title="资质证书" defaultOpen={false}>
          <p>二级建筑工程施工总承包</p>
        </DocumentCard>,
      ),
    )
    // Mounted (not display:none'd away) but in the closed grid state.
    expect(container.textContent).toContain('二级建筑工程施工总承包')
    const collapse = container.querySelector('.card-collapse')
    expect(collapse?.classList.contains('open')).toBe(false)

    const toggle = container.querySelector('button[aria-expanded="false"]')!
    expect(toggle).toBeTruthy()
    act(() => toggle.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(container.querySelector('.card-collapse')?.classList.contains('open')).toBe(true)
    expect(container.querySelector('button[aria-expanded="true"]')).toBeTruthy()
  })

  it('defaults open and renders the count badge', () => {
    act(() =>
      root.render(
        <DocumentCard title="资质证书" count={3}>
          <p>x</p>
        </DocumentCard>,
      ),
    )
    expect(container.querySelector('.card-collapse')?.classList.contains('open')).toBe(true)
    expect(container.textContent).toContain('3')
  })
})
