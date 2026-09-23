package zioapp.backend.http

import zio.*
import zio.http.*

/** The aspects the route files wrap their `Routes` in. */
object RouteSupport {

  /** Rejects a request without a session. */
  val authenticated: HandlerAspect[Any, Unit] = {
    HandlerAspect.interceptIncomingHandler(
      Handler.fromFunctionZIO[Request] { (request: Request) =>
        if (hasSession(request)) ZIO.succeed((request, ()))
        else ZIO.fail(Response.unauthorized)
      }
    )
  }

  /** Rejects a request without an administrator's session. */
  val adminOnly: HandlerAspect[Any, Unit] = {
    HandlerAspect.interceptIncomingHandler(
      Handler.fromFunctionZIO[Request] { (request: Request) =>
        if (hasSession(request) && request.headers.get("X-Admin").contains("1")) ZIO.succeed((request, ()))
        else ZIO.fail(Response.forbidden)
      }
    )
  }

  /** Looks at the session if there is one; never rejects. */
  val optionalUser: HandlerAspect[Any, Unit] = {
    HandlerAspect.interceptIncomingHandler(
      Handler.fromFunctionZIO[Request]((request: Request) => ZIO.succeed((request, ())))
    )
  }

  /** Rejects a mutating request without the CSRF header. */
  val csrf: HandlerAspect[Any, Unit] = {
    HandlerAspect.interceptIncomingHandler(
      Handler.fromFunctionZIO[Request] { (request: Request) =>
        if (request.method == Method.GET || request.headers.get("X-Requested-With").isDefined) ZIO.succeed((request, ()))
        else ZIO.fail(Response.forbidden)
      }
    )
  }

  val requestLogging: HandlerAspect[Any, Unit] = HandlerAspect.identity

  /** Answers every failure with its response. */
  def handleFailures[Env](routes: Routes[Env, Response]): Routes[Env, Nothing] = routes.handleError(identity)

  private def hasSession(request: Request): Boolean = request.cookie("session").isDefined
}
