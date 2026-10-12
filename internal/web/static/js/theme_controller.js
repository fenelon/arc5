import { Controller } from "./stimulus.js"

// Switches between the light and dark theme and remembers the choice in a
// cookie, so the server renders the right theme on the next page.
export default class extends Controller {
  toggle() {
    const root = document.documentElement
    const dark = root.dataset.theme
      ? root.dataset.theme === "dark"
      : window.matchMedia("(prefers-color-scheme: dark)").matches
    const next = dark ? "light" : "dark"
    root.dataset.theme = next
    document.cookie = `arc5_theme=${next}; path=/; max-age=31536000; samesite=lax`
  }
}
