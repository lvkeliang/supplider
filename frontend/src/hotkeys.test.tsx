// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { hotkeyAction, isEditableTarget } from './hotkeys'

// TR-18: global shortcut rules. jsdom supplies HTMLElement/input targets.
describe('hotkeyAction', () => {
  it('maps Ctrl/Cmd+N to new (not while editing a field)', () => {
    expect(hotkeyAction({ key: 'n', ctrlKey: true }, document.body)).toBe('new')
    expect(hotkeyAction({ key: 'N', metaKey: true }, document.body)).toBe('new')
    const input = document.createElement('input')
    expect(hotkeyAction({ key: 'n', ctrlKey: true }, input)).toBeNull()
  })

  it('maps Ctrl/Cmd+K to search even inside a field', () => {
    const input = document.createElement('textarea')
    expect(hotkeyAction({ key: 'k', ctrlKey: true }, input)).toBe('search')
    expect(hotkeyAction({ key: 'K', metaKey: true }, document.body)).toBe('search')
  })

  it('maps "/" to search only when not editing', () => {
    expect(hotkeyAction({ key: '/' }, document.body)).toBe('search')
    const input = document.createElement('input')
    expect(hotkeyAction({ key: '/' }, input)).toBeNull()
    // shift+/ (typing "?") must not trigger
    expect(hotkeyAction({ key: '/', shiftKey: true }, document.body)).toBeNull()
  })

  it('maps Esc to back only when not editing', () => {
    expect(hotkeyAction({ key: 'Escape' }, document.body)).toBe('back')
    const sel = document.createElement('select')
    expect(hotkeyAction({ key: 'Escape' }, sel)).toBeNull()
  })

  it('ignores ordinary keys and Alt-modified chords', () => {
    expect(hotkeyAction({ key: 'n' }, document.body)).toBeNull()
    expect(hotkeyAction({ key: 'Enter' }, document.body)).toBeNull()
    expect(hotkeyAction({ key: 'n', altKey: true, ctrlKey: true }, document.body)).toBeNull()
  })
})

describe('isEditableTarget', () => {
  it('recognises form controls and contentEditable', () => {
    expect(isEditableTarget(null)).toBe(false)
    expect(isEditableTarget(document.body)).toBe(false)
    expect(isEditableTarget(document.createElement('input'))).toBe(true)
    expect(isEditableTarget(document.createElement('textarea'))).toBe(true)
    expect(isEditableTarget(document.createElement('select'))).toBe(true)
    const div = document.createElement('div')
    div.setAttribute('contenteditable', 'true')
    document.body.appendChild(div) // jsdom computes isContentEditable only when attached
    expect(isEditableTarget(div)).toBe(true)
    div.remove()
  })
})
