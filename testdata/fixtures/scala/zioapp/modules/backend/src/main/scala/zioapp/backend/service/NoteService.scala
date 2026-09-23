package zioapp.backend.service

import zio.*
import zioapp.backend.db.{NoteRepository, NoteRow}
import zioapp.shared.{CreateNote, Note, NoteError, validTitle}

/** Notes, as the routes see them. */
trait NoteService {
  def list: Task[List[Note]]

  def find(id: Long): Task[Option[Note]]

  def create(in: CreateNote): Task[Either[NoteError, Note]]
}

object NoteService {
  def list: RIO[NoteService, List[Note]] =
    ZIO.serviceWithZIO[NoteService](_.list)

  def create(in: CreateNote): RIO[NoteService, Either[NoteError, Note]] =
    ZIO.serviceWithZIO[NoteService](_.create(in))

  val live: URLayer[NoteRepository, NoteService] =
    ZLayer.fromFunction((repo: NoteRepository) => NoteServiceLive(repo): NoteService)
}

final case class NoteServiceLive(repo: NoteRepository) extends NoteService {

  def list: Task[List[Note]] = repo.all.map(_.map(toNote))

  def find(id: Long): Task[Option[Note]] = repo.find(id).map(_.map(toNote))

  def create(in: CreateNote): Task[Either[NoteError, Note]] = {
    if (!validTitle(in.title))
      ZIO.succeed(Left(NoteError.EmptyTitle))
    else
      repo.insert(in.title, in.body).map(row => Right(toNote(row)))
  }

  private def toNote(row: NoteRow): Note = Note(row.id, row.title, row.body)
}
