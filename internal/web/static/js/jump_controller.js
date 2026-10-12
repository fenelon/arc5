import { Controller } from "./stimulus.js"

// The phone's year picker jumps as soon as a year is chosen. Without this
// controller the "Go" button does the same.
export default class extends Controller {
  static targets = ["button"]

  connect() {
    this.buttonTarget.hidden = true
  }

  go() {
    this.element.requestSubmit()
  }
}
