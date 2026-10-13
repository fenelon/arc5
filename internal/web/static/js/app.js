import { Application } from "./stimulus.js"
import ThemeController from "./theme_controller.js"
import PickerController from "./picker_controller.js"
import ScrollController from "./scroll_controller.js"
import JumpController from "./jump_controller.js"
import SearchController from "./search_controller.js"

const application = Application.start()
application.register("theme", ThemeController)
application.register("picker", PickerController)
application.register("scroll", ScrollController)
application.register("jump", JumpController)
application.register("search", SearchController)
