import { Application } from "./stimulus.js"
import ThemeController from "./theme_controller.js"
import PickerController from "./picker_controller.js"
import ScrollController from "./scroll_controller.js"
import JumpController from "./jump_controller.js"
import SearchController from "./search_controller.js"
import ProgressController from "./progress_controller.js"
import PagerController from "./pager_controller.js"
import RailController from "./rail_controller.js"
import ListController from "./list_controller.js"
import PlaceController from "./place_controller.js"

const application = Application.start()
application.register("theme", ThemeController)
application.register("picker", PickerController)
application.register("scroll", ScrollController)
application.register("jump", JumpController)
application.register("search", SearchController)
application.register("progress", ProgressController)
application.register("pager", PagerController)
application.register("rail", RailController)
application.register("list", ListController)
application.register("place", PlaceController)
