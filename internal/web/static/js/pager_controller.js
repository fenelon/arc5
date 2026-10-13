import { Controller } from "./stimulus.js"

// Loads earlier and later messages in place as the reader scrolls toward
// either end of a conversation. The "Earlier messages" and "Later messages"
// links still work without it: this fetches the same address with
// part=messages, which answers with the page's messages alone.
export default class extends Controller {
  static targets = ["older", "newer"]

  connect() {
    // Start loading well before the reader reaches the end of what is shown.
    this.observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) this.load(entry.target)
      }
    }, { root: this.element, rootMargin: "1200px 0px" })
    for (const link of [...this.olderTargets, ...this.newerTargets]) this.observer.observe(link)
  }

  disconnect() {
    this.observer?.disconnect()
    this.pending?.abort()
  }

  olderTargetConnected(link) { this.observer?.observe(link) }
  newerTargetConnected(link) { this.observer?.observe(link) }
  olderTargetDisconnected(link) { this.observer?.unobserve(link) }
  newerTargetDisconnected(link) { this.observer?.unobserve(link) }

  async load(link) {
    if (this.busy) return
    this.busy = true
    const older = link.dataset.pagerTarget === "older"
    const label = link.textContent
    link.textContent = "Loading…"
    link.setAttribute("aria-busy", "true")
    const url = new URL(link.href, window.location.origin)
    url.searchParams.set("part", "messages")
    this.pending = new AbortController()
    try {
      const response = await fetch(url, { signal: this.pending.signal, credentials: "same-origin" })
      if (response.redirected) {
        window.location.assign(response.url) // signed out meanwhile
        return
      }
      if (!response.ok) throw new Error(`status ${response.status}`)
      const page = document.createElement("template")
      page.innerHTML = await response.text() // escaped by the server's templates; a template runs nothing
      if (older) this.prepend(link, page.content)
      else this.append(link, page.content)
    } catch {
      link.textContent = label // offline or refused: the link still works by hand
      link.removeAttribute("aria-busy")
    } finally {
      this.busy = false
    }
    // A short page may leave the next link in view already: the observer
    // reports it only when it enters, so look again.
    requestAnimationFrame(() => this.again())
  }

  // prepend puts an earlier page above link, keeping what the reader sees
  // where it is.
  prepend(link, page) {
    // Measure the first message shown before anything above it changes.
    const anchor = this.element.querySelector(".msg, .service")
    const before = anchor?.getBoundingClientRect().top
    page.querySelector('[data-pager-target="newer"]')?.remove()
    const days = page.querySelectorAll("h2.day")
    const first = this.element.querySelector("h2.day")
    if (days.length && first && days[days.length - 1].dataset.day === first.dataset.day) first.remove()
    link.replaceWith(page)
    if (anchor) this.element.scrollTop += anchor.getBoundingClientRect().top - before
  }

  // append puts a later page below link.
  append(link, page) {
    page.querySelector('[data-pager-target="older"]')?.remove()
    const day = page.querySelector("h2.day")
    const days = this.element.querySelectorAll("h2.day")
    if (day && days.length && days[days.length - 1].dataset.day === day.dataset.day) day.remove()
    link.replaceWith(page)
  }

  again() {
    const root = this.element.getBoundingClientRect()
    for (const link of [...this.olderTargets, ...this.newerTargets]) {
      const box = link.getBoundingClientRect()
      if (box.bottom > root.top - 1200 && box.top < root.bottom + 1200) {
        this.load(link)
        return
      }
    }
  }
}
