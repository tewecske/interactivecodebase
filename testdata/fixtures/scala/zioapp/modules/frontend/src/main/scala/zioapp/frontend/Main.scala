package zioapp.frontend

import com.raquo.laminar.api.L.*
import org.scalajs.dom

object Main {
  def main(args: Array[String]): Unit = {
    renderOnDomContentLoaded(dom.document.getElementById("app"), App.render())
  }
}
