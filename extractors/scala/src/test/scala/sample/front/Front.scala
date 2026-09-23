package sample.front

// A Laminar frontend routed by Waypoint (see src/test/scala/stubs) that the
// extractor tests read the TASTy of; it calls the API of sample.web.

import com.raquo.laminar.nodes.ReactiveElement
import com.raquo.waypoint.*
import sample.web.{Api, Paths, Template, Verb}

sealed trait Page

object Page {
  case object Home extends Page
  final case class Item(label: String, itemId: Long) extends Page
  case object Start extends Page
}

object Locale {
  def prefix: String = sys.props.getOrElse("locale", "/en")
}

object FrontPaths {
  val rename = Template(Verb.PUT, "/api/items/{itemName}")
  val search = Template(Verb.GET, "/api/search")
}

object AppRouter {
  import Page.*

  private val basePath = Locale.prefix

  private val homeRoute = Route.static(Home, root / "home", basePath)

  private val itemRoute = Route(
    encode = (p: Item) => (p.label, p.itemId),
    decode = (args: (String, Long)) => Item(args._1, args._2),
    pattern = root / "items" / segment[Long] / "as" / segment[String],
    basePath = basePath,
  )

  // Only decodes: links to Home use homeRoute.
  private val startRoute = Route.applyPF[Home.type, Unit](
    matchEncode = PartialFunction.empty,
    decode = { case _ => Home },
    pattern = root,
    basePath = basePath,
  )

  private val pageRoute = Route.static(Start, root / "start", basePath)

  val router = new Router[Page](List(homeRoute, itemRoute, startRoute, pageRoute))
}

object Client {
  def item(): String = call(Paths.item)
  def count(): String = endpoint(Api.count)
  def rename(): String = call(FrontPaths.rename)
  def search(): String = call(FrontPaths.search)

  private def call(t: Template): String = t.path
  private def endpoint(e: Any): String = e.toString
}

object Views {
  import AppRouter.router

  def shell(content: ReactiveElement): ReactiveElement = ReactiveElement(link(Page.Home), content)

  private def link(page: Page): String = router.navigateTo(page)

  def home(): ReactiveElement = shell(ReactiveElement(Client.count(), router.relativeUrlForPage(Page.Item("a", 1L))))

  def item(id: Long): ReactiveElement = {
    shell(ReactiveElement(Client.item(), Client.search(), () => { Client.rename(); router.pushState(Page.Home) }))
  }

  def start(): ReactiveElement = ReactiveElement()
}

object App {
  def renderers(page: Page): SplitRender[Page, ReactiveElement] = {
    SplitRender[Page, ReactiveElement](page)
      .collectStatic(Page.Home)(Views.home())
      .collectSignal[Page.Item](p => Views.item(p.itemId))
      .collectStaticPF { case p => renderPage(p) }
  }

  private def renderPage(page: Page): ReactiveElement = page match {
    case Page.Start => Views.start()
    case _          => ReactiveElement()
  }
}
