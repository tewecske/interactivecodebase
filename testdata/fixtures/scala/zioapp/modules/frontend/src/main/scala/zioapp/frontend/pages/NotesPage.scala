package zioapp.frontend.pages

import com.raquo.laminar.api.L.*
import zioapp.frontend.{AppRouter, Page}
import zioapp.frontend.api.NoteApi

object NotesPage {

  def render(): HtmlElement = {
    Shell(
      div(
        child.text <-- NoteApi.list(),
        a(AppRouter.router.navigateTo(Page.NoteDetail(1L)), "first note"),
      )
    )
  }
}
