package zioapp.frontend.api

import com.raquo.airstream.web.FetchStream
import com.raquo.laminar.api.L.*
import org.scalajs.dom
import zio.http.endpoint.Endpoint
import zioapp.shared.api.{ApiCall, ApiMethod}

/** Makes the requests, like gathedge's HttpClient: the method and path come
  * from the shared API paths.
  */
object HttpClient {

  def call(c: ApiCall): EventStream[String] = send(verb(c.method), c.path)

  /** A request for an endpoint's path, filled in by the caller. */
  def endpoint(e: Endpoint[?, ?, ?, ?, ?], path: String): EventStream[String] = send(_.GET, path)

  private def send(method: dom.HttpMethod.type => dom.HttpMethod, path: String): EventStream[String] = {
    FetchStream.raw(method, path).flatMapSwitch(resp => EventStream.fromJsPromise(resp.text()))
  }

  private def verb(method: ApiMethod): dom.HttpMethod.type => dom.HttpMethod = method match {
    case ApiMethod.GET    => _.GET
    case ApiMethod.POST   => _.POST
    case ApiMethod.PUT    => _.PUT
    case ApiMethod.DELETE => _.DELETE
  }
}
