import { Controller } from "./stimulus.js"

// Opens a conversation where the reader is going: at the newest message, at
// the top of a page of later messages, or at the message the page was opened
// at. Without it the page simply starts at the top.
export default class extends Controller {
  static values = { to: String }

  connect() {
    if (this.toValue === "end") {
      this.toEnd()
      // Pictures load after this runs and push the newest message down.
      // Follow them, unless the reader has scrolled up meanwhile.
      this.onScroll = () => {
        const el = this.element
        this.atEnd = el.scrollHeight - el.scrollTop - el.clientHeight < 40
      }
      this.onLoad = () => {
        if (this.atEnd) this.toEnd()
      }
      this.element.addEventListener("scroll", this.onScroll, { passive: true })
      this.element.addEventListener("load", this.onLoad, true) // load does not bubble; catch it on the way down
    } else if (this.toValue !== "start") {
      const target = document.getElementById(this.toValue)
      if (target) target.scrollIntoView({ block: "center" })
    }
  }

  disconnect() {
    if (this.onScroll) this.element.removeEventListener("scroll", this.onScroll)
    if (this.onLoad) this.element.removeEventListener("load", this.onLoad, true)
  }

  toEnd() {
    this.element.scrollTop = this.element.scrollHeight
    this.atEnd = true
  }
}
