package zioapp.backend.http

import zio.*
import zio.http.*
import zio.json.*
import zioapp.backend.service.NoteService
import zioapp.shared.{CreateNote, NoteError}

/** The notes API. */
object NoteRoutes {

  val routes: Routes[NoteService, Nothing] = {
    Routes(
      Method.GET / "api" / "notes" -> handler { (_: Request) =>
        NoteService.list.map(notes => Response.json(notes.toJson)).orDie
      },
      Method.GET / "api" / "notes" / long("id") -> handler { (id: Long, _: Request) =>
        ZIO.serviceWithZIO[NoteService](_.find(id)).map(toResponse).orDie
      },
      Method.POST / "api" / "notes" -> handler { (req: Request) =>
        create(req).orDie
      },
    )
  }

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

  private def toResponse(note: Option[zioapp.shared.Note]): Response = {
    note.fold(Response.notFound)(n => Response.json(n.toJson))
  }
}
