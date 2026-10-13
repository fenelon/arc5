import { Controller } from "./stimulus.js"

// Scrolls a long year rail to the quarter being read, which is usually the
// newest, at the bottom.
export default class extends Controller {
  connect() {
    const band = this.element.querySelector(".band--now")
    if (!band) {
      this.element.scrollTop = this.element.scrollHeight
      return
    }
    const rail = this.element.getBoundingClientRect()
    const box = band.getBoundingClientRect()
    this.element.scrollTop += box.top - rail.top - (rail.height - box.height) / 2
  }
}
