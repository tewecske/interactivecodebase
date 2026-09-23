package zioapp.backend

import zio.*
import zio.http.*
import zioapp.backend.db.{DataSourceFactory, NoteRepository}
import zioapp.backend.http.NoteRoutes
import zioapp.backend.service.NoteService

object Main extends ZIOAppDefault {

  override def run: ZIO[Any, Throwable, Unit] = {
    Server
      .serve(NoteRoutes.routes)
      .provide(Server.default, NoteService.live, NoteRepository.live, DataSourceFactory.live)
  }
}
