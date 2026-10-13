import { Controller } from "./stimulus.js"

const key = "arc5:place"

// Keeps a page of forms where it was after one is saved: the page that comes
// back shows the saved section at the same height on the screen.
export default class extends Controller {
  connect() {
    let saved = null
    try {
      saved = JSON.parse(sessionStorage.getItem(key))
      sessionStorage.removeItem(key)
    } catch {}
    if (!saved || saved.path !== window.location.pathname) return
    const section = document.getElementById(saved.id)
    if (!section) return
    // After the browser's own jump to the address's #section, if any.
    requestAnimationFrame(() => window.scrollBy(0, section.getBoundingClientRect().top - saved.top))
  }

  remember(event) {
    const section = event.target.closest("[id]")
    if (!section || !this.element.contains(section)) return
    try {
      sessionStorage.setItem(key, JSON.stringify({ path: window.location.pathname, id: section.id, top: section.getBoundingClientRect().top }))
    } catch {}
  }
}
