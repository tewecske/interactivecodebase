// Stand-ins for the parts of Laminar and Waypoint the frontend tests use:
// both are Scala.js libraries, so the sample in src/test/scala/sample/front
// compiles against these on the JVM. Names, owners and parameter names
// follow the real APIs; the bodies do nothing.

package com.raquo.laminar.nodes {

  class ReactiveElement

  object ReactiveElement {
    def apply(children: Any*): ReactiveElement = new ReactiveElement
  }
}

package com.raquo.waypoint {

  import scala.language.implicitConversions
  import scala.reflect.ClassTag

  /** A url-dsl path pattern; the stand-in does not track its arguments. */
  final class PathSegment[+A] {
    def /[B](next: PathSegment[B]): PathSegment[Nothing] = new PathSegment[Nothing]
    def ?[Q](params: Q): PathSegment[A] = this
  }

  implicit def unaryPathSegment(s: String): PathSegment[Unit] = new PathSegment[Unit]

  val root: PathSegment[Unit] = new PathSegment[Unit]

  def segment[A]: PathSegment[A] = new PathSegment[A]

  class Route[Page, Args]

  object Route {
    def static[Page](staticPage: Page, pattern: PathSegment[Unit], basePath: String = ""): Route[Page, Unit] = new Route

    def apply[Page, Args](encode: Page => Args, decode: Args => Page, pattern: PathSegment[Args], basePath: String = "")(using
      ClassTag[Page]
    ): Route[Page, Args] = new Route

    def applyPF[Page, Args](
      matchEncode: PartialFunction[Any, Args],
      decode: PartialFunction[Args, Page],
      pattern: PathSegment[Args],
      basePath: String = "",
    ): Route[Page, Args] = new Route
  }

  class Router[Page](routes: List[Route[? <: Page, ?]]) {
    def navigateTo(page: Page, replaceState: Boolean = false): String = ""
    def pushState(page: Page): Unit = ()
    def replaceState(page: Page): Unit = ()
    def relativeUrlForPage(page: Page): String = ""
  }

  class SplitRender[Page, View](current: Page) {
    def collectStatic[P <: Page](page: P)(view: => View): SplitRender[Page, View] = this
    def collectSignal[P <: Page](render: P => View)(using ClassTag[P]): SplitRender[Page, View] = this
    def collectStaticPF(pf: PartialFunction[Page, View]): SplitRender[Page, View] = this
  }
}
