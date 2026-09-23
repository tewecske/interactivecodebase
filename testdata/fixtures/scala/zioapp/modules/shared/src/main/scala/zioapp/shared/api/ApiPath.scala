package zioapp.shared.api

/** The HTTP methods the API uses. */
enum ApiMethod {
  case GET, POST, PUT, DELETE
}

/** A request to make: a method and a filled-in path. */
final case class ApiCall(method: ApiMethod, path: String)

/** One endpoint's method and path template, like gathedge's: written once,
  * filled in by a client, turned into a zio-http route by [[ApiRoutes]].
  */
sealed trait ApiPath {
  def method: ApiMethod
  def template: String
}

final case class ApiPath0(method: ApiMethod, template: String) extends ApiPath {
  def apply(): ApiCall = ApiCall(method, template)
}

final case class ApiPath1[A](method: ApiMethod, template: String) extends ApiPath {
  def apply(a: A): ApiCall = ApiCall(method, template.replaceFirst("\\{[^/]+\\}", a.toString))
}
