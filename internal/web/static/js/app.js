import { Application } from "./stimulus.js"
import ThemeController from "./theme_controller.js"
import PickerController from "./picker_controller.js"

const application = Application.start()
application.register("theme", ThemeController)
application.register("picker", PickerController)
