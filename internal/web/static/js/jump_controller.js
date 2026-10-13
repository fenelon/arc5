import { Controller } from "./stimulus.js"

// Submits its form as soon as a choice changes: the phone's year picker and
// the theme in Settings. Without this controller their button does the same.
export default class extends Controller {
  static targets = ["button"]

  connect() {
    this.buttonTarget.hidden = true
  }

  go() {
    this.element.requestSubmit()
  }
}
