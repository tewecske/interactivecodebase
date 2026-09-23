package zioapp.shared.api

import zio.http.*
import zio.http.codec.PathCodec
import zio.http.endpoint.Endpoint

/** The method and path of the calls a client makes, written once. */
object NotePaths {

  import ApiMethod.*

  val stats = ApiPath0(GET, "/api/admin/stats")
  val archive = ApiPath1[Long](POST, "/api/notes/{id}/archive")
}

/** Descriptions of the notes API that the backend implements. */
object NoteEndpoints {

  private val notes = "notes"

  val tags = Endpoint(Method.GET / "api" / notes / long("id") / "tags").out[List[String]]

  val stats = Endpoint(ApiRoutes.route0(NotePaths.stats)).out[String]

  val archive = Endpoint(ApiRoutes.route1(NotePaths.archive, PathCodec.long)).out[String]
}
