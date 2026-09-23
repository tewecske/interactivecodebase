package zioapp.shared.api

/** The HTTP methods the API uses. */
enum ApiMethod {
  case GET, POST, PUT, DELETE
}

/** One endpoint's method and path template, like gathedge's: written once,
  * filled in by a client, turned into a zio-http route by [[ApiRoutes]].
  */
sealed trait ApiPath {
  def method: ApiMethod
  def template: String
}

final case class ApiPath0(method: ApiMethod, template: String) extends ApiPath

final case class ApiPath1[A](method: ApiMethod, template: String) extends ApiPath
