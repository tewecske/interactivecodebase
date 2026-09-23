package zioapp.backend.service

import zio.*
import zio.http.*
import zioapp.shared.Note

/** Tells another service about new notes. */
object Webhook {
  private val url = "https://hooks.example.com/notes"

  def notify(client: Client, note: Note): Task[Status] = {
    ZIO.scoped {
      client.batched(Request.post(url, Body.fromString(note.title))).map(_.status)
    }
  }
}
