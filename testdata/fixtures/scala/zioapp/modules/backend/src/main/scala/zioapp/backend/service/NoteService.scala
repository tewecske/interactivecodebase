package zioapp.backend.service

import zio.*
import zioapp.backend.db.{NoteRepository, NoteRow}
import zioapp.shared.{CreateNote, Note, NoteError, validTitle}

/** Notes, as the routes see them. */
trait NoteService {
  def list: Task[List[Note]]

  def find(id: Long): Task[Option[Note]]

  def create(in: CreateNote): Task[Either[NoteError, Note]]

  def remove(id: Long): Task[Boolean]
}

object NoteService {
  def list: RIO[NoteService, List[Note]] =
    ZIO.serviceWithZIO[NoteService](_.list)

  def create(in: CreateNote): RIO[NoteService, Either[NoteError, Note]] =
    ZIO.serviceWithZIO[NoteService](_.create(in))

  def remove(id: Long): RIO[NoteService, Boolean] =
    ZIO.serviceWithZIO[NoteService](_.remove(id))

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

  def remove(id: Long): Task[Boolean] = repo.remove(id).map(_ > 0)

  private def toNote(row: NoteRow): Note = Note(row.id, row.title, row.body)
}
