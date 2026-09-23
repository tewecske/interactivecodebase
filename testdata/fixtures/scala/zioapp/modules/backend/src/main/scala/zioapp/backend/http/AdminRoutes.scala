package zioapp.backend.http

import zio.*
import zio.http.*
import zioapp.backend.service.NoteService
import zioapp.shared.api.NoteEndpoints

/** Administration, implemented against the shared `NoteEndpoints`. */
object AdminRoutes {

  private val statsRoute = {
    NoteEndpoints.stats.implementHandler(handler((_: Unit) => NoteService.list.map(n => s"${n.size} notes").orDie))
  }

  private val archiveRoute = NoteEndpoints.archive.implementHandler(handler(archive))

  val routes: Routes[NoteService, Response] = Routes(statsRoute, archiveRoute) @@ RouteSupport.adminOnly

  private def archive(id: Long): UIO[String] = ZIO.succeed(s"archived $id")
}
