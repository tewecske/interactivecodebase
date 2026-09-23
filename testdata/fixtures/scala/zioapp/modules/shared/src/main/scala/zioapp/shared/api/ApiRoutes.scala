package zioapp.shared.api

import zio.http.{Method, RoutePattern}
import zio.http.codec.PathCodec

/** Turns an [[ApiPath]] into the zio-http route an `Endpoint` is built on;
  * the path is only known at run time.
  */
object ApiRoutes {

  def route0(p: ApiPath0): RoutePattern[Unit] = {
    val (List(r0), _) = split(p): @unchecked
    method(p) / literals(r0)
  }

  def route1[A](p: ApiPath1[A], a: String => PathCodec[A]): RoutePattern[A] = {
    val (List(r0, r1), List(n0)) = split(p): @unchecked
    method(p) / (literals(r0) / a(n0) / literals(r1))
  }

  private def method(p: ApiPath): Method = Method.fromString(p.method.toString)

  private def split(p: ApiPath): (List[List[String]], List[String]) = {
    val segments = p.template.split('/').toList.filter(_.nonEmpty)
    segments.foldRight((List(List.empty[String]), List.empty[String])) { case (s, (runs, names)) =>
      if (s.startsWith("{")) (Nil :: runs, s.drop(1).dropRight(1) :: names)
      else ((s :: runs.head) :: runs.tail, names)
    }
  }

  private def literals(run: List[String]): PathCodec[Unit] =
    run.foldLeft(PathCodec.empty)((codec, s) => codec / PathCodec.literal(s))
}
