package sample.web

// zio-http routes the extractor tests read the TASTy of.

import zio.*
import zio.http.*
import zio.http.codec.PathCodec
import zio.http.endpoint.Endpoint

enum Verb {
  case GET, POST
}

/** A method and path template, like gathedge's ApiPath. */
final case class Template(method: Verb, path: String)

object Paths {
  val item = Template(Verb.GET, "/api/items/{id}")
}

object Api {
  def route(t: Template): RoutePattern[Unit] = Method.fromString(t.method.toString) / PathCodec.literal(t.path)

  val item = Endpoint(route(Paths.item)).out[String]

  val count = Endpoint(Method.GET / "api" / "count").out[Int]
}

object Aspects {
  val requireUser: HandlerAspect[Any, Unit] = HandlerAspect.identity
  val staff: HandlerAspect[Any, Unit] = HandlerAspect.identity
  val audit: HandlerAspect[Any, Unit] = HandlerAspect.identity
}

object Web {
  private val prefix = "api"

  private val open = Routes(
    Method.GET / prefix / "items" / int("page") -> handler { (page: Int, _: Request) =>
      Response.text(render(page))
    },
    Api.count.implementHandler(handler((_: Unit) => ZIO.succeed(1))),
  )

  private val closed = Routes(
    Method.PUT / prefix / "items" / string("name") -> handler(update),
    Api.item.implement(_ => ZIO.succeed(render(0))),
  ) @@ Aspects.requireUser

  private val staffOnly = Routes(Method.DELETE / prefix / "items" / s"${prefix.length}" -> Handler.ok) @@ Aspects.staff

  val routes: Routes[Any, Response] = (open ++ closed ++ staffOnly) @@ Aspects.audit

  private def update(name: String, req: Request): UIO[Response] = ZIO.succeed(Response.text(render(name.length)))

  private def render(n: Int): String = n.toString
}
