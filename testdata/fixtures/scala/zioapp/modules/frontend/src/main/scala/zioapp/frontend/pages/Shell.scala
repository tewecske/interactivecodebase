package zioapp.frontend.pages

import com.raquo.laminar.api.L.*
import zioapp.frontend.{AppRouter, Page}

/** The layout every page shares: a navigation bar and the content. */
object Shell {

  def apply(content: HtmlElement): HtmlElement = {
    div(
      navTag(navLink(Page.Notes, "Notes"), navLink(Page.Admin, "Admin")),
      content,
    )
  }

  private def navLink(page: Page, label: String): HtmlElement = {
    a(AppRouter.router.navigateTo(page), label)
  }
}
