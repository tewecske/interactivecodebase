package zioapp.shared

import zio.json.*

final case class Note(id: Long, title: String, body: String) derives JsonCodec

final case class CreateNote(title: String, body: String) derives JsonCodec

/** Why a note operation failed. */
sealed trait NoteError

object NoteError {
  final case class NotFound(id: Long) extends NoteError
  case object EmptyTitle extends NoteError
}
