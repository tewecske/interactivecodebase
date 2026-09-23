package zioapp.frontend.pages

import com.raquo.laminar.api.L.*
import zioapp.frontend.api.NoteApi

object AdminPage {

  def render(): HtmlElement = {
    Shell(div(child.text <-- NoteApi.stats(), child.text <-- NoteApi.search()))
  }
}
