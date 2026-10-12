import { Controller } from "./stimulus.js"

// The picker is a <details> element and works without this. The controller
// closes it on Escape and on a click anywhere else.
export default class extends Controller {
  connect() {
    this.onClick = (event) => {
      if (!this.element.contains(event.target)) this.element.open = false
    }
    this.onKey = (event) => {
      if (event.key === "Escape") this.element.open = false
    }
    document.addEventListener("click", this.onClick)
    document.addEventListener("keydown", this.onKey)
  }

  disconnect() {
    document.removeEventListener("click", this.onClick)
    document.removeEventListener("keydown", this.onKey)
  }
}
