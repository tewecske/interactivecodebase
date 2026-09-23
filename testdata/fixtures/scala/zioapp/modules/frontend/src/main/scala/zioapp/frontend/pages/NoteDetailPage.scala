package zioapp.frontend.pages

import com.raquo.laminar.api.L.*
import zioapp.frontend.{AppRouter, Page}
import zioapp.frontend.api.NoteApi

object NoteDetailPage {

  def render(id: Long): HtmlElement = {
    Shell(
      div(
        child.text <-- NoteApi.get(id),
        child.text <-- NoteApi.tags(id),
        button(
          "Archive",
          onClick.flatMapTo(NoteApi.archive(id)) --> { _ => AppRouter.router.pushState(Page.Notes) },
        ),
        a(href := AppRouter.router.relativeUrlForPage(Page.Notes), "back"),
      )
    )
  }
}
