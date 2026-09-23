package zioapp.backend

import zio.*
import zio.http.*
import zioapp.backend.db.{DataSourceFactory, NoteRepository}
import zioapp.backend.http.{AdminRoutes, NoteRoutes, RouteSupport}
import zioapp.backend.service.NoteService

object Main extends ZIOAppDefault {

  private val health = Routes(Method.GET / "health" -> Handler.ok)

  private val allRoutes = {
    val combined = NoteRoutes.routes ++ AdminRoutes.routes ++ health
    RouteSupport.handleFailures(combined) @@ RouteSupport.requestLogging
  }

  private def port: Int = sys.env.get("PORT").flatMap(_.toIntOption).getOrElse(8080)

  override def run: ZIO[Any, Throwable, Unit] = {
    Server
      .serve(allRoutes)
      .provide(Server.defaultWithPort(port), NoteService.live, NoteRepository.live, DataSourceFactory.live)
  }
}
