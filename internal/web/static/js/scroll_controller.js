import { Controller } from "./stimulus.js"

// Opens a conversation where the reader is going: at the newest message, at
// the top of a page of later messages, or at the message the page was opened
// at. Without it the page simply starts at the top.
export default class extends Controller {
  static values = { to: String }

  connect() {
    if (this.toValue === "end") {
      this.element.scrollTop = this.element.scrollHeight
    } else if (this.toValue !== "start") {
      const target = document.getElementById(this.toValue)
      if (target) target.scrollIntoView({ block: "center" })
    }
  }
}
