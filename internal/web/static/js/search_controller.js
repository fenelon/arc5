import { Controller } from "./stimulus.js"

// The filters a search keeps while the text changes.
const filters = ["sort", "files", "deleted", "service", "from", "to"]

// Searches as the owner types. The form works without this controller; with
// it, the left column is replaced by results a moment after typing stops,
// and the address keeps the search so a reload or the back button returns
// to it.
export default class extends Controller {
  static targets = ["input"]
  static values = { results: String }

  disconnect() {
    clearTimeout(this.timer)
    this.pending?.abort()
  }

  type() {
    clearTimeout(this.timer)
    this.timer = setTimeout(() => this.load(), 250)
  }

  submit(event) {
    event.preventDefault()
    clearTimeout(this.timer)
    this.load()
  }

  clear() {
    this.inputTarget.value = ""
    this.load()
  }

  async load() {
    const q = this.inputTarget.value
    const url = new URL(this.resultsValue, window.location.origin)
    url.searchParams.set("q", q)
    const current = new URLSearchParams(window.location.search)
    for (const name of filters) {
      if (current.has(name)) url.searchParams.set(name, current.get(name))
    }
    this.pending?.abort()
    this.pending = new AbortController()
    let response
    try {
      response = await fetch(url, { signal: this.pending.signal, credentials: "same-origin" })
    } catch {
      return // replaced by a newer search, or offline
    }
    if (response.redirected) {
      window.location.assign(response.url) // signed out meanwhile
      return
    }
    if (!response.ok) return
    const list = document.getElementById("list")
    if (!list) return
    list.innerHTML = await response.text() // escaped by the server's templates
    list.scrollTop = 0
    const here = new URL(window.location.href)
    for (const name of ["q", "page", "person", "chats"]) here.searchParams.delete(name)
    if (q.trim() !== "") here.searchParams.set("q", q)
    window.history.replaceState(null, "", here)
  }
}
