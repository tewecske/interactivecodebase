package zioapp.backend.db

import io.getquill.*
import zio.*

import javax.sql.DataSource

final case class NoteRow(id: Long, title: String, body: String)

/** The notes table. */
trait NoteRepository {
  def all: Task[List[NoteRow]]

  def find(id: Long): Task[Option[NoteRow]]

  def insert(title: String, body: String): Task[NoteRow]

  def rename(id: Long, title: String): Task[Long]

  def remove(id: Long): Task[Long]
}

object NoteRepository {
  val live: ZLayer[DataSource, Nothing, NoteRepository] =
    ZLayer.fromFunction((ds: DataSource) => new NoteRepositoryLive(ds): NoteRepository)
}

final class NoteRepositoryLive(dataSource: DataSource)
    extends QuillRepository(dataSource, new PostgresZioJdbcContext(SnakeCase))
    with NoteRepository {
  import ctx.*

  private inline def notes = quote(querySchema[NoteRow]("notes"))

  def all: Task[List[NoteRow]] = run(ctx.run(notes.sortBy(_.id)))

  def find(id: Long): Task[Option[NoteRow]] = {
    val q = quote(notes.filter(n => n.id == lift(id)))
    run(ctx.run(q)).map(_.headOption)
  }

  def insert(title: String, body: String): Task[NoteRow] = {
    val q = quote {
      notes.insertValue(lift(NoteRow(0L, title, body))).returningGenerated(_.id)
    }
    run(ctx.run(q)).map(id => NoteRow(id, title, body))
  }

  def rename(id: Long, title: String): Task[Long] = {
    run(ctx.run(notes.filter(_.id == lift(id)).update(_.title -> lift(title))))
  }

  def remove(id: Long): Task[Long] = {
    val q = quote(notes.filter(n => n.id == lift(id)).delete)
    run(ctx.run(q))
  }
}
