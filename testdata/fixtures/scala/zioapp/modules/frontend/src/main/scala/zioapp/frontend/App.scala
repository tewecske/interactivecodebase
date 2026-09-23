package zioapp.frontend

import com.raquo.laminar.api.L.*
import com.raquo.waypoint.SplitRender
import zioapp.frontend.pages.{AdminPage, NoteDetailPage, NotesPage}

/** Renders the current page: two through SplitRender, one through a match. */
object App {

  def render(): HtmlElement = {
    div(child <-- renderers.signal)
  }

  private def renderers: SplitRender[Page, HtmlElement] = {
    SplitRender[Page, HtmlElement](AppRouter.router.currentPageSignal)
      .collectStatic(Page.Notes)(NotesPage.render())
      .collectSignal[Page.NoteDetail](page => div(child <-- page.map(p => NoteDetailPage.render(p.noteId))))
      .collectStaticPF { case page => renderPage(page) }
  }

  private def renderPage(page: Page): HtmlElement = {
    page match {
      case Page.Admin => AdminPage.render()
      case _          => div("not found")
    }
  }
}
