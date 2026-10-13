import { Controller } from "./stimulus.js"

// Keeps a part of the page current: every few seconds, while the page is in
// view, it fetches the page again and swaps in the element with the same id.
// The server renders and escapes it, as for a normal page load.
export default class extends Controller {
  static values = { every: Number }

  connect() {
    this.timer = setInterval(() => this.refresh(), this.everyValue || 5000)
  }

  disconnect() {
    clearInterval(this.timer)
  }

  async refresh() {
    if (document.hidden || this.busy) return
    this.busy = true
    try {
      const response = await fetch(window.location.href, { credentials: "same-origin" })
      if (!response.ok || response.redirected) return
      const page = new DOMParser().parseFromString(await response.text(), "text/html")
      const fresh = page.getElementById(this.element.id)
      if (fresh) this.element.innerHTML = fresh.innerHTML
    } catch {
      // offline for a moment; try again next time
    } finally {
      this.busy = false
    }
  }
}
