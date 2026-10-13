import { Controller } from "./stimulus.js"

const key = "arc5:list"

// Keeps the chat list where it was when a chat is opened from it: the new
// page starts the list at the same place, with the open chat in view.
export default class extends Controller {
  connect() {
    let saved = null
    try {
      saved = JSON.parse(sessionStorage.getItem(key))
      sessionStorage.removeItem(key)
    } catch {}
    if (saved && saved.path === this.listPath()) this.element.scrollTop = saved.top
    const current = this.element.querySelector(".row--current")
    if (!current) return
    const list = this.element.getBoundingClientRect()
    const row = current.getBoundingClientRect()
    if (row.top < list.top || row.bottom > list.bottom) current.scrollIntoView({ block: "center" })
  }

  remember(event) {
    if (!event.target.closest("a")) return
    try {
      sessionStorage.setItem(key, JSON.stringify({ path: this.listPath(), top: this.element.scrollTop }))
    } catch {}
  }

  // listPath names the list being shown: the source, and the search if any.
  listPath() {
    const here = new URL(window.location.href)
    const source = here.pathname.split("/").slice(0, 3).join("/")
    return source + "?" + (here.searchParams.get("q") || "")
  }
}
