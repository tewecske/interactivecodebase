package zioapp.frontend

import com.raquo.waypoint.*

/** The frontend's pages, like gathedge's. */
sealed trait Page

object Page {
  case object Notes extends Page
  final case class NoteDetail(noteId: Long) extends Page
  case object Admin extends Page
}

object AppRouter {

  import Page.*

  private val basePath = "/app"

  private val notesRoute = Route.static(Notes, root / "notes", basePath)

  private val noteRoute = Route(
    encode = (p: NoteDetail) => p.noteId,
    decode = (id: Long) => NoteDetail(id),
    pattern = root / "notes" / segment[Long],
    basePath = basePath,
  )

  private val adminRoute = Route.static(Admin, root / "admin", basePath)

  val router: Router[Page] = new Router[Page](
    routes = List(notesRoute, noteRoute, adminRoute),
    serializePage = {
      case NoteDetail(id) => s"note:$id"
      case other          => other.toString
    },
    deserializePage = s => if (s == "Admin") Admin else Notes,
    getPageTitle = _ => "Notes",
    routeFallback = _ => Notes,
  )
}
