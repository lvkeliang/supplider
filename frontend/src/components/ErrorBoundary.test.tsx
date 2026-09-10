// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, type ReactElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { ErrorBoundary } from './ErrorBoundary'

// jsdom component test for TR-12: an uncaught render error must show the
// fallback (not a blank page), and 重试 must remount the subtree.
function Boom({ message }: { message: string }): ReactElement {
  throw new Error(message)
}

function Good(): ReactElement {
  return <div>正常内容</div>
}

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  // React logs real errors for expected boundary catches — silence them.
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.restoreAllMocks()
})

describe('ErrorBoundary', () => {
  it('renders children when nothing throws', () => {
    act(() => root.render(<ErrorBoundary><Good /></ErrorBoundary>))
    expect(container.textContent).toContain('正常内容')
  })

  it('shows a friendly fallback when a child render throws', () => {
    act(() =>
      root.render(
        <ErrorBoundary>
          <Boom message="坏数据形状" />
        </ErrorBoundary>,
      ),
    )
    expect(container.textContent).toContain('页面出现了一点问题')
    expect(container.textContent).toContain('重试')
    expect(container.textContent).toContain('坏数据形状')
    expect(container.textContent).not.toContain('正常内容')
  })

  it('remounts the subtree on retry (recovers when the error is gone)', () => {
    let shouldThrow = true
    const MaybeBoom = (): ReactElement => {
      if (shouldThrow) throw new Error('临时错误')
      return <div>恢复后的内容</div>
    }
    act(() => root.render(<ErrorBoundary><MaybeBoom /></ErrorBoundary>))
    expect(container.textContent).toContain('页面出现了一点问题')

    shouldThrow = false
    const retryBtn = Array.from(container.querySelectorAll('button')).find((b) =>
      b.textContent?.includes('重试'),
    )!
    expect(retryBtn).toBeTruthy()
    act(() => retryBtn.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(container.textContent).toContain('恢复后的内容')
    expect(container.textContent).not.toContain('页面出现了一点问题')
  })
})
