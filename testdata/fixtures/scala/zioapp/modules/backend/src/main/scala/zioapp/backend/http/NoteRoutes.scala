package zioapp.backend.http

import zio.*
import zio.http.*
import zio.http.codec.PathCodec
import zio.json.*
import zioapp.backend.service.NoteService
import zioapp.shared.{CreateNote, NoteError}
import zioapp.shared.api.NoteEndpoints

/** The notes API: reading is public, writing needs a session. */
object NoteRoutes {

  private val notes = "notes"

  private val base = Method.GET / "api" / notes

  private val publicRoutes: Routes[NoteService, Response] = {
    Routes(
      base -> handler { (_: Request) =>
        NoteService.list.map(notes => Response.json(notes.toJson)).orDie
      },
      Method.GET / "api" / notes / long("id") -> handler { (id: Long, _: Request) =>
        ZIO.serviceWithZIO[NoteService](_.find(id)).map(toResponse).orDie
      },
      Method.GET / "api" / "files" / string("owner") / trailing -> Handler.notFound,
    ) @@ RouteSupport.optionalUser
  }

  private val tagsRoute = {
    NoteEndpoints.tags.implementHandler(handler((id: Long) => ZIO.succeed(List(s"note-$id"))))
  }

  private val sessionRoutes: Routes[NoteService, Response] = {
    Routes(
      Method.POST / "api" / notes -> handler { (req: Request) =>
        create(req).orDie
      },
      Method.DELETE / "api" / notes / PathCodec.uuid("key") -> handler(remove),
      tagsRoute,
    ) @@ RouteSupport.authenticated
  }

  val routes: Routes[NoteService, Response] = (publicRoutes ++ sessionRoutes) @@ RouteSupport.csrf

  private def create(req: Request): ZIO[NoteService, Throwable, Response] = {
    for {
      body <- req.body.asString
      in   <- ZIO.fromEither(body.fromJson[CreateNote]).mapError(new IllegalArgumentException(_))
      out  <- NoteService.create(in)
    } yield out match {
      case Right(note)                => Response.json(note.toJson).status(Status.Created)
      case Left(NoteError.EmptyTitle) => Response.badRequest("empty title")
      case Left(NoteError.NotFound(_)) => Response.notFound
    }
  }

  private def remove(key: java.util.UUID, req: Request): URIO[NoteService, Response] = {
    NoteService.remove(key.getLeastSignificantBits).map(if (_) Response.ok else Response.notFound).orDie
  }

  private def toResponse(note: Option[zioapp.shared.Note]): Response = {
    note.fold(Response.notFound)(n => Response.json(n.toJson))
  }
}
