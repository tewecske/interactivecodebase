package zioapp.frontend.api

import com.raquo.laminar.api.L.*
import zioapp.shared.api.{NoteEndpoints, NotePaths}

/** The notes API as the pages call it. */
object NoteApi {

  def list(): EventStream[String] = HttpClient.call(NotePaths.list())

  def get(id: Long): EventStream[String] = HttpClient.call(NotePaths.get(id))

  def archive(id: Long): EventStream[String] = HttpClient.call(NotePaths.archive(id))

  def tags(id: Long): EventStream[String] = HttpClient.endpoint(NoteEndpoints.tags, s"/api/notes/$id/tags")

  def stats(): EventStream[String] = HttpClient.call(NotePaths.stats())

  def search(): EventStream[String] = HttpClient.call(NotePaths.search())
}
